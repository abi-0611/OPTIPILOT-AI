// -- Tenant types -------------------------------------------------------------
export interface TenantState {
  name: string;
  tier: string;
  current_cores: number;
  current_memory_gib: number;
  current_cost_usd: number;
  max_cores: number;
  max_memory_gib: number;
  max_monthly_cost_usd: number;
  guaranteed_cores_percent: number;
  burstable: boolean;
  fairness_score: number;
  allocation_status: string;
  last_refreshed: string;
  is_noisy: boolean;
  is_victim: boolean;
}

export interface FairnessResponse {
  timestamp: string;
  global_index: number;
  per_tenant: Record<string, number>;
}

export interface UsageResponse {
  tenant: string;
  current_cores: number;
  current_memory_gib: number;
  current_cost_usd: number;
  max_cores: number;
  max_memory_gib: number;
  time_series?: Array<{ timestamp: string; value: number }>;
}

// -- SLO Status types ---------------------------------------------------------
export interface SLOStatus {
  compliant: boolean;
  burn_rate: number;
  budget_remaining: number;
  latency_p99: number;
  error_rate: number;
  availability: number;
  throughput: number;
}

// -- Decision types ------------------------------------------------------------
export interface DecisionRecord {
  id: string;
  timestamp: string;
  namespace: string;
  service: string;
  trigger: string;
  action_type: string;
  dry_run: boolean;
  confidence: number;
  slo_status?: SLOStatus;
  candidates?: CandidatePlan[];
  selected_action?: ScalingAction;
  objective_weights?: Record<string, number>;
}

export interface CandidatePlan {
  replicas: number;
  cpu_request: string;
  memory_request: string;
  spot_ratio: number;
  estimated_cost: number;
  estimated_carbon: number;
}

export interface ScalingAction {
  type: string;
  target_replica: number;
  cpu_request: string;
  memory_request: string;
  spot_ratio: number;
  dry_run: boolean;
  reason: string;
  confidence: number;
}

export interface JournalStats {
  total_decisions: number;
  decisions_per_hour: number;
  average_confidence: number;
  top_triggers: Array<{ trigger: string; count: number }>;
  top_services: Array<{ service: string; count: number }>;
}

// -- Simulation types ----------------------------------------------------------
export interface SimulationRequest {
  services: string[];
  start: string;
  end: string;
  step?: number;
  description?: string;
}

export interface CostSummary {
  total_hourly_cost: number;
  avg_hourly_cost: number;
  peak_hourly_cost: number;
}

export interface SimulationResult {
  id: string;
  description: string;
  start: string;
  end: string;
  duration: string;
  original_cost: CostSummary;
  simulated_cost: CostSummary;
  cost_delta_percent: number;
  original_slo_breaches: number;
  simulated_slo_breaches: number;
  total_steps: number;
}

export interface CurvePoint {
  slo_target: number;
  projected_monthly_cost: number;
  projected_compliance_percent: number;
  avg_replicas: number;
  slo_breaches: number;
  total_steps: number;
}

export interface SLOCostCurveResponse {
  service: string;
  slo_metric: string;
  points: CurvePoint[];
}