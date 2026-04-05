# ── Stage 1: build dependencies ───────────────────────────────────────────────
FROM python:3.11-slim AS builder

WORKDIR /build

# Install build tools needed for native extensions (numpy, pandas, etc.)
RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc \
    g++ \
    && rm -rf /var/lib/apt/lists/*

# Copy dependency manifests first for better layer caching.
COPY requirements.txt .

# Install runtime dependencies into a prefix directory.
RUN pip install --no-cache-dir --prefix=/install -r requirements.txt

# ── Stage 2: runtime image ─────────────────────────────────────────────────────
FROM python:3.11-slim AS runtime

WORKDIR /app

# Copy installed packages from builder stage.
COPY --from=builder /install /usr/local

# Copy the application source.
COPY app/ ./app/

# Non-root user for security.
RUN addgroup --system --gid 1001 optipilot \
    && adduser --system --uid 1001 --gid 1001 --no-create-home optipilot
USER optipilot

# Expose the service port.
EXPOSE 8080

# Environment defaults (override via K8s env or ConfigMap).
ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    SPOT_MODEL_PATH=""

# Start the FastAPI service with uvicorn.
CMD ["python", "-m", "uvicorn", "app.main:create_app", \
     "--factory", \
     "--host", "0.0.0.0", \
     "--port", "8080", \
     "--workers", "1", \
     "--log-level", "info"]
