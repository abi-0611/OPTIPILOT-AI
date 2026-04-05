package explainability

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/optipilot-ai/optipilot/internal/engine"

	_ "modernc.org/sqlite"
)

const createTableSQL = `
CREATE TABLE IF NOT EXISTS decisions (
    id TEXT PRIMARY KEY,
    timestamp DATETIME NOT NULL,
    namespace TEXT NOT NULL,
    service TEXT NOT NULL,
    trigger TEXT NOT NULL,
    record JSON NOT NULL,
    action_type TEXT,
    dry_run BOOLEAN,
    confidence REAL,
    reason TEXT DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_decisions_ns_svc ON decisions(namespace, service);
CREATE INDEX IF NOT EXISTS idx_decisions_time ON decisions(timestamp);
CREATE INDEX IF NOT EXISTS idx_decisions_trigger ON decisions(trigger);
`

const createFTSSQL = `
CREATE VIRTUAL TABLE IF NOT EXISTS decisions_fts USING fts5(
    decision_id UNINDEXED, namespace, service, trigger, action_type, reason
);
`

const migrateReasonSQL = `ALTER TABLE decisions ADD COLUMN reason TEXT DEFAULT ''`

// Journal persists DecisionRecords to a SQLite database.
type Journal struct {
	db     *sql.DB
	mu     sync.Mutex
	hasFTS bool // true if FTS5 virtual table was created successfully
}

// NewJournal creates a Journal backed by a SQLite database at the given path.
// Use ":memory:" for an in-memory database (useful in tests).
func NewJournal(dbPath string) (*Journal, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("opening sqlite: %w", err)
	}

	// Enable WAL for better concurrent read performance.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("setting WAL mode: %w", err)
	}

	if _, err := db.Exec(createTableSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("creating schema: %w", err)
	}

	// Migration: add reason column if missing (pre-existing databases).
	db.Exec(migrateReasonSQL) //nolint:errcheck // ignore if column already exists

	// Try to create FTS5 virtual table (optional — degrades gracefully to LIKE).
	_, ftsErr := db.Exec(createFTSSQL)

	return &Journal{db: db, hasFTS: ftsErr == nil}, nil
}

// Write persists a DecisionRecord to the journal.
func (j *Journal) Write(record engine.DecisionRecord) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	recordJSON, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshaling record: %w", err)
	}

	reason := record.SelectedAction.Reason

	_, err = j.db.Exec(
		`INSERT INTO decisions (id, timestamp, namespace, service, trigger, record, action_type, dry_run, confidence, reason)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID,
		record.Timestamp.UTC().Format(time.RFC3339Nano),
		record.Namespace,
		record.Service,
		record.Trigger,
		string(recordJSON),
		string(record.ActionType),
		record.DryRun,
		record.Confidence,
		reason,
	)
	if err != nil {
		return fmt.Errorf("inserting decision: %w", err)
	}

	if j.hasFTS {
		j.db.Exec( //nolint:errcheck
			`INSERT INTO decisions_fts (decision_id, namespace, service, trigger, action_type, reason) VALUES (?, ?, ?, ?, ?, ?)`,
			record.ID, record.Namespace, record.Service, record.Trigger, string(record.ActionType), reason,
		)
	}

	return nil
}

// QueryFilter defines filter parameters for querying decisions.
type QueryFilter struct {
	Namespace string
	Service   string
	Trigger   string
	Since     *time.Time
	Limit     int
}

// Query returns decision records matching the filter criteria.
func (j *Journal) Query(filter QueryFilter) ([]engine.DecisionRecord, error) {
	query := "SELECT record FROM decisions WHERE 1=1"
	args := []interface{}{}

	if filter.Namespace != "" {
		query += " AND namespace = ?"
		args = append(args, filter.Namespace)
	}
	if filter.Service != "" {
		query += " AND service = ?"
		args = append(args, filter.Service)
	}
	if filter.Trigger != "" {
		query += " AND trigger = ?"
		args = append(args, filter.Trigger)
	}
	if filter.Since != nil {
		query += " AND timestamp >= ?"
		args = append(args, filter.Since.UTC().Format(time.RFC3339Nano))
	}

	query += " ORDER BY timestamp DESC"

	if filter.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, filter.Limit)
	}

	rows, err := j.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying decisions: %w", err)
	}
	defer rows.Close()

	var records []engine.DecisionRecord
	for rows.Next() {
		var recordJSON string
		if err := rows.Scan(&recordJSON); err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}
		var record engine.DecisionRecord
		if err := json.Unmarshal([]byte(recordJSON), &record); err != nil {
			return nil, fmt.Errorf("unmarshaling record: %w", err)
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

// GetByID retrieves a single decision record by ID.
func (j *Journal) GetByID(id string) (*engine.DecisionRecord, error) {
	var recordJSON string
	err := j.db.QueryRow("SELECT record FROM decisions WHERE id = ?", id).Scan(&recordJSON)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying decision %s: %w", id, err)
	}

	var record engine.DecisionRecord
	if err := json.Unmarshal([]byte(recordJSON), &record); err != nil {
		return nil, fmt.Errorf("unmarshaling record: %w", err)
	}
	return &record, nil
}

// Search performs a full-text search across decision namespace, service, trigger,
// action_type, and reason fields. Uses FTS5 if available, falls back to LIKE.
func (j *Journal) Search(text string, limit int) ([]engine.DecisionRecord, error) {
	if text == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 50
	}

	var rows *sql.Rows
	var err error

	if j.hasFTS {
		ftsQuery := `"` + strings.ReplaceAll(text, `"`, `""`) + `"`
		rows, err = j.db.Query(
			`SELECT d.record FROM decisions d
			 INNER JOIN decisions_fts f ON d.id = f.decision_id
			 WHERE decisions_fts MATCH ?
			 ORDER BY d.timestamp DESC LIMIT ?`,
			ftsQuery, limit,
		)
	} else {
		pattern := "%" + text + "%"
		rows, err = j.db.Query(
			`SELECT record FROM decisions
			 WHERE namespace LIKE ? OR service LIKE ? OR trigger LIKE ? OR action_type LIKE ? OR reason LIKE ?
			 ORDER BY timestamp DESC LIMIT ?`,
			pattern, pattern, pattern, pattern, pattern, limit,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("searching decisions: %w", err)
	}
	defer rows.Close()

	var records []engine.DecisionRecord
	for rows.Next() {
		var recordJSON string
		if err := rows.Scan(&recordJSON); err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}
		var record engine.DecisionRecord
		if err := json.Unmarshal([]byte(recordJSON), &record); err != nil {
			return nil, fmt.Errorf("unmarshaling record: %w", err)
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

// TriggerCount holds a trigger value and its occurrence count.
type TriggerCount struct {
	Trigger string `json:"trigger"`
	Count   int    `json:"count"`
}

// ServiceCount holds a service name and its actuation count.
type ServiceCount struct {
	Service string `json:"service"`
	Count   int    `json:"count"`
}

// JournalStats holds aggregated statistics for a time window.
type JournalStats struct {
	TotalDecisions    int            `json:"total_decisions"`
	DecisionsPerHour  float64        `json:"decisions_per_hour"`
	AverageConfidence float64        `json:"average_confidence"`
	TopTriggers       []TriggerCount `json:"top_triggers"`
	TopServices       []ServiceCount `json:"top_services"`
}

// AggregateStats computes summary statistics for decisions within the given time window.
func (j *Journal) AggregateStats(window time.Duration) (*JournalStats, error) {
	since := time.Now().UTC().Add(-window).Format(time.RFC3339Nano)
	stats := &JournalStats{}

	err := j.db.QueryRow(
		`SELECT COUNT(*), COALESCE(AVG(confidence), 0) FROM decisions WHERE timestamp >= ?`, since,
	).Scan(&stats.TotalDecisions, &stats.AverageConfidence)
	if err != nil {
		return nil, fmt.Errorf("aggregate count: %w", err)
	}

	hours := window.Hours()
	if hours > 0 && stats.TotalDecisions > 0 {
		stats.DecisionsPerHour = float64(stats.TotalDecisions) / hours
	}

	// Top triggers (up to 5).
	trigRows, err := j.db.Query(
		`SELECT trigger, COUNT(*) as cnt FROM decisions WHERE timestamp >= ?
		 GROUP BY trigger ORDER BY cnt DESC LIMIT 5`, since,
	)
	if err != nil {
		return nil, fmt.Errorf("aggregate triggers: %w", err)
	}
	defer trigRows.Close()
	for trigRows.Next() {
		var tc TriggerCount
		if err := trigRows.Scan(&tc.Trigger, &tc.Count); err != nil {
			return nil, err
		}
		stats.TopTriggers = append(stats.TopTriggers, tc)
	}
	if err := trigRows.Err(); err != nil {
		return nil, err
	}

	// Top services by actuation count (up to 5).
	svcRows, err := j.db.Query(
		`SELECT service, COUNT(*) as cnt FROM decisions WHERE timestamp >= ?
		 GROUP BY service ORDER BY cnt DESC LIMIT 5`, since,
	)
	if err != nil {
		return nil, fmt.Errorf("aggregate services: %w", err)
	}
	defer svcRows.Close()
	for svcRows.Next() {
		var sc ServiceCount
		if err := svcRows.Scan(&sc.Service, &sc.Count); err != nil {
			return nil, err
		}
		stats.TopServices = append(stats.TopServices, sc)
	}
	return stats, svcRows.Err()
}

// Purge deletes decisions older than the given time. Returns the number of rows deleted.
func (j *Journal) Purge(olderThan time.Time) (int64, error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	ts := olderThan.UTC().Format(time.RFC3339Nano)

	if j.hasFTS {
		j.db.Exec( //nolint:errcheck
			`DELETE FROM decisions_fts WHERE decision_id IN (SELECT id FROM decisions WHERE timestamp < ?)`, ts,
		)
	}

	result, err := j.db.Exec(`DELETE FROM decisions WHERE timestamp < ?`, ts)
	if err != nil {
		return 0, fmt.Errorf("purging decisions: %w", err)
	}
	return result.RowsAffected()
}

// Close closes the underlying database connection.
func (j *Journal) Close() error {
	return j.db.Close()
}
