#Requires -Version 5.1
<#
.SYNOPSIS
  Checks prerequisites for OptiPilot proactive (forecast-driven) autoscaling.

.PARAMETER Namespace
  Namespace where the cluster-agent runs (default: optipilot-system).
#>
param([string] $Namespace = "optipilot-system")

$ErrorActionPreference = "Continue"
function Ok($msg) { Write-Host "[OK] $msg" -ForegroundColor Green }
function Warn($msg) { Write-Host "[WARN] $msg" -ForegroundColor Yellow }

Write-Host "`n=== OptiPilot proactive scaling checks ===`n" -ForegroundColor Cyan

$pod = kubectl -n $Namespace get pods -l app.kubernetes.io/name=cluster-agent -o jsonpath='{.items[0].metadata.name}' 2>$null
if (-not $pod) {
  Write-Host "[FAIL] No cluster-agent pod in $Namespace" -ForegroundColor Red
  exit 1
}
Ok "cluster-agent pod: $pod"

$logs = kubectl -n $Namespace logs $pod -c manager --tail=120 2>$null
if ($logs -match "Prometheus verified") {
  Ok "Startup log contains: Prometheus verified"
} elseif ($logs -match "WARNING:.*Prometheus") {
  Warn "Prometheus check failed — verify --prometheus-url and network from the pod"
} else {
  Warn "Rebuild/redeploy manager to see 'Prometheus verified' in logs"
}

Write-Host "`nManual checks:" -ForegroundColor Cyan
Write-Host "  1) kubectl -n $Namespace port-forward svc/<cluster-agent-svc> 8090:8090"
Write-Host "  2) curl http://localhost:8090/api/v1/decisions?limit=5  (look for forecastState)"
Write-Host "  3) curl http://localhost:8090/metrics | findstr optipilot_optimizer_forecast_attachment"
Write-Host ""
