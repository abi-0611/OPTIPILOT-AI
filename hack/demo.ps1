# ============================================================
# OptiPilot AI - Live Presentation Demo Script
# Run from: optipilot-ai repo (or any path); example:  cd optipilot-ai;  . .\hack\demo.ps1
# Requirements: Docker Desktop running, kind cluster up
#
# Easiest autoscale review (HTTP load + watch):  . .\hack\demo.ps1
#                                                 Run-TrafficDemo
# Optional: Run-TrafficDemo -IncludeExtraTargets
# ============================================================

$CTX = "kind-optipilot-quickstart"
$K   = "kubectl --context $CTX"
# Namespace where your demo workload runs (Online Boutique often uses "default")
$NS  = "default"
# Deployment used in Demo-HorizontalScaling (scale to 0); must match a ServiceObjective targetRef.name
$DemoHorizontalDeployment = "frontend"
# Kubernetes service name for REST/What-If examples (matches targetRef.name / journal)
$DemoServiceNameForAPI = "frontend"
# In-cluster HTTP URL for load generators (Boutique: Service frontend exposes port 80 -> container 8080)
$script:DemoLoadHttpUrl = "http://frontend/"
# Optional extra URLs for additional loadgen pods when -IncludeExtraTargets is used (second pod hits same URL = more RPS)
$script:DemoLoadHttpUrlsExtra = @("http://frontend/")
# Optional: deployments Reset-Cluster restores to 1 replica (Boutique core path)
$script:DemoRestoreDeployments = @('frontend', 'checkoutservice', 'cartservice')
# Repo root (parent of hack/) when script lives at hack/demo.ps1
$RepoRoot = if ($PSScriptRoot) { (Resolve-Path (Join-Path $PSScriptRoot '..')).Path } else { (Get-Location).Path }

function Get-FirstDemoServiceObjectiveName {
    kubectl --context $CTX get serviceobjectives -n $NS -o jsonpath='{.items[0].metadata.name}' 2>$null
}
function Get-FirstDemoOptimizationPolicyName {
    kubectl --context $CTX get optimizationpolicies -n $NS -o jsonpath='{.items[0].metadata.name}' 2>$null
}

# ── helpers ─────────────────────────────────────────────────
function pause-section ($title) {
    Write-Host ""
    Write-Host ("=" * 60) -ForegroundColor Cyan
    Write-Host "  $title" -ForegroundColor Cyan
    Write-Host ("=" * 60) -ForegroundColor Cyan
    Write-Host "  Press ENTER to continue..." -ForegroundColor Yellow
    Read-Host | Out-Null
}

function kube ($args_) { Invoke-Expression "kubectl --context $CTX $args_" }
function show ($label) { Write-Host "`n>>> $label" -ForegroundColor Green }

# ── WSL2 zombie watchdog ────────────────────────────────────
# Detects the zombie state and auto-recovers before it
# cascades into TLS handshake timeouts and Docker 500 errors.
function Test-DockerHealth {
    <#
    .SYNOPSIS
        Returns $true if Docker engine is responsive, $false otherwise.
        If the WSL2 VM is a zombie (listed Running but unresponsive),
        automatically kills it and restarts Docker Desktop.
    #>

    # Quick check: is Docker CLI responsive?
    $dockerOk = $false
    try {
        $info = docker info --format '{{.ServerVersion}}' 2>&1
        if ($LASTEXITCODE -eq 0 -and $info -match '^\d') { $dockerOk = $true }
    } catch {}

    if ($dockerOk) { return $true }

    # Docker is unresponsive — check if WSL2 VM is a zombie
    Write-Host "[watchdog] Docker engine unresponsive. Checking WSL2 VM..." -ForegroundColor Yellow
    $wslState = wsl --list --verbose 2>&1 | Select-String "docker-desktop"
    if ($wslState -match "Running") {
        # VM says Running — try to exec into it
        $probe = wsl -d docker-desktop -- cat /proc/uptime 2>&1
        if ($LASTEXITCODE -ne 0) {
            Write-Host "[watchdog] WSL2 docker-desktop VM is ZOMBIE. Auto-recovering..." -ForegroundColor Red
            wsl --shutdown 2>$null
            Start-Sleep -Seconds 3

            # Restart Docker Desktop
            $dd = Get-Process "Docker Desktop" -ErrorAction SilentlyContinue
            if ($dd) { $dd | Stop-Process -Force; Start-Sleep -Seconds 2 }

            Start-Process "$env:ProgramFiles\Docker\Docker\Docker Desktop.exe"
            Write-Host "[watchdog] Docker Desktop restarting. Waiting for engine..." -ForegroundColor Yellow

            # Poll until Docker responds (max 90s)
            $waited = 0
            while ($waited -lt 90) {
                Start-Sleep -Seconds 5; $waited += 5
                try {
                    $v = docker info --format '{{.ServerVersion}}' 2>&1
                    if ($LASTEXITCODE -eq 0 -and $v -match '^\d') {
                        Write-Host "[watchdog] Docker engine recovered ($waited`s)." -ForegroundColor Green
                        return $true
                    }
                } catch {}
                Write-Host "[watchdog] waiting... ($waited`s)" -ForegroundColor DarkGray
            }
            Write-Host "[watchdog] Docker did not recover in 90s. Please restart manually." -ForegroundColor Red
            return $false
        }
    }

    # VM is Stopped or absent — just need Docker Desktop running
    Write-Host "[watchdog] WSL2 VM is stopped. Starting Docker Desktop..." -ForegroundColor Yellow
    Start-Process "$env:ProgramFiles\Docker\Docker\Docker Desktop.exe"
    $waited = 0
    while ($waited -lt 90) {
        Start-Sleep -Seconds 5; $waited += 5
        try {
            $v = docker info --format '{{.ServerVersion}}' 2>&1
            if ($LASTEXITCODE -eq 0 -and $v -match '^\d') {
                Write-Host "[watchdog] Docker engine started ($waited`s)." -ForegroundColor Green
                return $true
            }
        } catch {}
    }
    return $false
}

# Wraps any kubectl call with Docker health pre-check
function Ensure-DockerReady {
    if (-not (Test-DockerHealth)) {
        Write-Host "Docker is not available. Cannot proceed." -ForegroundColor Red
        return $false
    }
    return $true
}

# ============================================================
# STEP 0 — CLUSTER HEALTH CHECK  (run this first, privately)
# ============================================================
function Check-ClusterHealth {
    if (-not (Ensure-DockerReady)) { return }

    show "Checking cluster health before demo..."
    docker ps --format "table {{.Names}}`t{{.Status}}" | Select-String "optipilot"
    kube "get nodes"
    $runningLines = kube "get pods -A --field-selector=status.phase=Running --no-headers" | Measure-Object -Line
    Write-Host ("Running pods (cluster-wide): {0}" -f $runningLines.Lines) -ForegroundColor DarkGray
    Write-Host "`nIf any nodes are NotReady, run: Reset-Cluster" -ForegroundColor Yellow
}

function Reset-Cluster {
    if (-not (Ensure-DockerReady)) { return }

    Write-Host "[1/5] Starting kind nodes..." -ForegroundColor Yellow
    docker start optipilot-quickstart-control-plane optipilot-quickstart-worker optipilot-quickstart-worker2 2>&1 | Out-Null

    Write-Host "[2/5] Waiting for nodes to become Ready (~30s)..." -ForegroundColor Yellow
    $deadline = (Get-Date).AddSeconds(120)
    do {
        Start-Sleep -Seconds 5
        $notReady = kubectl --context $CTX get nodes --no-headers 2>$null | Select-String "NotReady"
        $total    = kubectl --context $CTX get nodes --no-headers 2>$null | Measure-Object -Line
    } until ((-not $notReady -and $total.Lines -ge 3) -or (Get-Date) -gt $deadline)
    kube "get nodes"

    Write-Host "[3/5] Waiting for system pods to start (~30s)..." -ForegroundColor Yellow
    Start-Sleep -Seconds 30

    Write-Host "[4/5] Restoring demo app replica counts (if `$script:DemoRestoreDeployments is set)..." -ForegroundColor Yellow
    foreach ($d in $script:DemoRestoreDeployments) {
        kube "scale deploy/$d -n $NS --replicas=1" 2>$null | Out-Null
    }
    Write-Host "[5/5] Waiting for workload rollouts (if any)..." -ForegroundColor Yellow
    foreach ($d in $script:DemoRestoreDeployments) {
        kube "rollout status deploy/$d -n $NS --timeout=90s" 2>$null | Out-Null
    }
    kube "rollout status deploy/optipilot-cluster-agent -n optipilot-system --timeout=120s"

    Write-Host "`nCluster reset complete. Run Check-ClusterHealth to verify." -ForegroundColor Green
}

# Cleanly pause everything between sessions (saves WSL2 memory)
function Suspend-Demo {
    Write-Host "Stopping kind containers (cluster state preserved)..." -ForegroundColor Yellow
    docker stop optipilot-quickstart-control-plane optipilot-quickstart-worker optipilot-quickstart-worker2 2>&1 | Out-Null
    Write-Host "All kind nodes stopped. Resume with: Reset-Cluster" -ForegroundColor Green
}

# Rebuild controller image and reload it into kind — run once after code changes
function Deploy-NewImage {
    if (-not (Ensure-DockerReady)) { return }

    Write-Host "Building optipilot-manager:quickstart..." -ForegroundColor Yellow
    Set-Location $RepoRoot
    docker build -t optipilot-manager:quickstart -f Dockerfile .
    if ($LASTEXITCODE -ne 0) { Write-Host "Build FAILED" -ForegroundColor Red; return }

    Write-Host "Loading image into kind cluster..." -ForegroundColor Yellow
    kind load docker-image optipilot-manager:quickstart --name optipilot-quickstart

    Write-Host "Restarting controller..." -ForegroundColor Yellow
    kube "rollout restart deploy/optipilot-cluster-agent -n optipilot-system"
    kube "rollout status deploy/optipilot-cluster-agent -n optipilot-system --timeout=120s"
    Write-Host "Controller updated and ready." -ForegroundColor Green
}

# ============================================================
# SECTION 1 — THE PLATFORM ARCHITECTURE
# ============================================================
function Demo-Architecture {
    pause-section "SECTION 1: Platform Architecture"

    show "3-node Kubernetes cluster (kind)"
    kube "get nodes -o wide"

    show "What is running: 3 layers across namespaces"
    kube "get pods -A --no-headers" | `
        Select-String "optipilot-system|$NS|monitoring|kube-system" | `
        Format-Table

    show "App layer - workloads in namespace $NS"
    kube "get deploy,svc -n $NS"

    show "Controller layer - OptiPilot agent (optipilot-system namespace)"
    kube "get deploy,svc -n optipilot-system"

    show "Observability layer - Prometheus stack (monitoring namespace)"
    kube "get pods -n monitoring --no-headers" | Select-String "Running"
}

# ============================================================
# SECTION 2 — CUSTOM RESOURCE DEFINITIONS
# ============================================================
function Demo-CRDs {
    pause-section "SECTION 2: OptiPilot Kubernetes Extensions (CRDs)"

    show "OptiPilot extends Kubernetes with 3 new resource types"
    kube "get crds" | Select-String "optipilot"

    show "ServiceObjectives - defines what healthy means per service"
    kube "get serviceobjectives -n $NS -o wide"

    show "OptimizationPolicies - CEL guardrails for the solver"
    kube "get optimizationpolicies -n $NS"

    $soName = Get-FirstDemoServiceObjectiveName
    if ($soName) {
        show "Live SLO details (first ServiceObjective in $NS)"
        kube "describe serviceobjective $soName -n $NS"
    } else {
        Write-Host "  (no ServiceObjectives in $NS - apply one for your workload deployment)" -ForegroundColor Yellow
    }

    $polName = Get-FirstDemoOptimizationPolicyName
    if ($polName) {
        show "Live policy (first OptimizationPolicy)"
        kube "get optimizationpolicy $polName -n $NS -o yaml"
    } else {
        Write-Host "  (no OptimizationPolicies in $NS)" -ForegroundColor Yellow
    }
}

# ============================================================
# SECTION 3 — HORIZONTAL AUTO-SCALING (LIVE)
# ============================================================
function Demo-HorizontalScaling {
    pause-section "SECTION 3: Horizontal Auto-Scaling (LIVE)"

    show "Current replica counts ($DemoHorizontalDeployment should have a matching ServiceObjective)"
    kube "get deploy -n $NS"

    # Trigger SLO violation: scale deployment to 0 so kube-state-metrics reports
    # replicas_available=0, which violates the availability SLO target.
    # OptiPilot will detect this and issue a scale_up decision autonomously.
    show "Triggering SLO violation: scaling $DemoHorizontalDeployment to 0 replicas..."
    kube "scale deploy/$DemoHorizontalDeployment -n $NS --replicas=0"
    Write-Host "$DemoHorizontalDeployment scaled to 0 - availability SLO will fire within 30s scrape" -ForegroundColor Yellow

    show "Waiting for OptiPilot optimizer cycle (~75s)..."
    Write-Host '  Phase 1 (0 to 30s):  Prometheus scrapes 0 available replicas' -ForegroundColor Cyan
    Write-Host '  Phase 2 (30 to 60s): SLO controller marks SLOCompliant=False' -ForegroundColor Cyan
    Write-Host '  Phase 3 (60 to 75s): Optimizer evaluates, issues scale_up decision' -ForegroundColor Cyan

    1..15 | ForEach-Object {
        Start-Sleep -Seconds 5
        $elapsed = $_ * 5
        Write-Host "  [$elapsed s elapsed]" -ForegroundColor DarkGray -NoNewline
        # Show SLO status at 35s mark
        if ($elapsed -eq 35) {
            $soWatch = Get-FirstDemoServiceObjectiveName
            $cond = ""
            if ($soWatch) {
                $cond = kubectl --context $CTX get serviceobjective $soWatch -n $NS `
                    -o jsonpath='{.status.conditions[?(@.type=="SLOCompliant")].status}' 2>$null
            }
            Write-Host "  SLOCompliant=$cond (first SO: $soWatch)" -ForegroundColor $(if ($cond -eq "False") { "Red" } else { "Green" })
        } else {
            Write-Host ""
        }
    }

    show "OptiPilot decisions (scale_up should appear)..."
    kube "get events -n $NS --sort-by='.lastTimestamp' --field-selector reason=OptimizationDecision" | `
        Select-String "scale_up|scale_down|tune" | Select-Object -Last 5

    show "Current replica count - OptiPilot has scaled up autonomously"
    kube "get deploy $DemoHorizontalDeployment -n $NS"

    show "Actuation confirmations"
    kube "get events -n $NS --sort-by='.lastTimestamp' --field-selector reason=Actuated" | Select-Object -Last 5
}

# ── Load generator pods (curl in-cluster) ───────────────────
$script:LoadgenNames = @('loadgen-demo', 'loadgen-demo-1', 'loadgen-demo-2', 'loadgen', 'loadgen-api')

function Remove-LoadgenPods {
    foreach ($name in $script:LoadgenNames) {
        kube "delete pod $name -n $NS --ignore-not-found=true --force --grace-period=0" 2>$null | Out-Null
    }
    Write-Host "  (removed loadgen pods if present)" -ForegroundColor DarkGray
}

<#
.SYNOPSIS
    Starts HTTP load against $script:DemoLoadHttpUrl (and optional extra URLs) inside the cluster.
.PARAMETER IncludeExtraTargets
    If set, starts a second pod hitting each URL in $script:DemoLoadHttpUrlsExtra
#>
function Start-DemoLoadGenerators {
    param(
        [switch]$IncludeExtraTargets
    )
    if (-not (Ensure-DockerReady)) { return }

    Remove-LoadgenPods
    Start-Sleep -Seconds 2

    $u = $script:DemoLoadHttpUrl
    $apiScript = "while true; do curl -sS -o /dev/null `"$u`"; done"

    show "Starting loadgen-demo (curl loop) -> $u"
    & kubectl --context $CTX run loadgen-demo --restart=Never -n $NS --image=curlimages/curl:8.5.0 -- sh -c $apiScript
    Start-Sleep -Seconds 2
    kube "wait --for=condition=Ready pod/loadgen-demo -n $NS --timeout=60s" 2>$null

    if ($IncludeExtraTargets -and $script:DemoLoadHttpUrlsExtra.Count -gt 0) {
        $i = 0
        foreach ($ex in $script:DemoLoadHttpUrlsExtra) {
            $i++
            $script = "while true; do curl -sS -o /dev/null `"$ex`"; done"
            $podName = "loadgen-demo-$i"
            show "Starting $podName -> $ex"
            & kubectl --context $CTX run $podName --restart=Never -n $NS --image=curlimages/curl:8.5.0 -- sh -c $script
            Start-Sleep -Seconds 2
            kube "wait --for=condition=Ready pod/$podName -n $NS --timeout=60s" 2>$null | Out-Null
        }
    }

    show "Load generators running:"
    kube "get pods -n $NS --no-headers" | Select-String "loadgen"
}

function Stop-DemoLoadGenerators {
    Remove-LoadgenPods
    Write-Host "Load generators stopped." -ForegroundColor Green
}

function Start-CodeproLoadGenerators {
    param([switch]$IncludeExtraTargets)
    Start-DemoLoadGenerators -IncludeExtraTargets:$IncludeExtraTargets
}
function Stop-CodeproLoadGenerators { Stop-DemoLoadGenerators }

function Ensure-PortForward8090 {
    $alive = $false
    try {
        $r = Invoke-WebRequest -Uri 'http://127.0.0.1:8090/api/v1/meta' -UseBasicParsing -TimeoutSec 2
        $alive = ($r.StatusCode -eq 200)
    } catch {}
    if (-not $alive) {
        Write-Host "Starting background port-forward :8090 -> optipilot-cluster-agent..." -ForegroundColor Yellow
        Start-Process -WindowStyle Hidden powershell.exe `
            "-NoProfile -Command kubectl --context $CTX port-forward -n optipilot-system svc/optipilot-cluster-agent 8090:8090"
        Start-Sleep -Seconds 4
    }
}

# ============================================================
# SECTION 3b — TRAFFIC GENERATOR + AUTOSCALE (easy review path)
# ============================================================
function Demo-TrafficAutoscale {
    param(
        [switch]$IncludeExtraTargets,
        [int]$WatchIterations = 18,
        [int]$WatchIntervalSec = 10
    )

    pause-section "SECTION 3b: Traffic load + watch scaling (automated)"

    if (-not (Ensure-DockerReady)) { return }

    # Helps kubectl/kubernetes event messages show "->" instead of mojibake (e.g. "ΓåÆ") in older Windows consoles.
    try {
        [Console]::OutputEncoding = [System.Text.Encoding]::UTF8
        $OutputEncoding = [System.Text.Encoding]::UTF8
    } catch {}

    show "Baseline - replica counts"
    kube "get deploy -n $NS -o custom-columns=NAME:.metadata.name,REPLICAS:.spec.replicas,READY:.status.readyReplicas"

    show "Starting HTTP load inside cluster (DemoLoadHttpUrl=$script:DemoLoadHttpUrl)"
    Start-DemoLoadGenerators -IncludeExtraTargets:$IncludeExtraTargets

    $watchMinutes = [int]($WatchIterations * $WatchIntervalSec / 60)
    show ("Watching deployments every {0}s ({1} min); Ctrl+C to stop early" -f $WatchIntervalSec, $watchMinutes)
    Write-Host "  Tip: OptiPilot needs SLO evaluation + optimizer cycles; first changes may take 1-3 min." -ForegroundColor DarkCyan
    Write-Host "  Event ages (e.g. 11m) only refresh when NEW decisions occur; cooldowns can mean no new rows during this window.`n" -ForegroundColor DarkCyan

    for ($i = 1; $i -le $WatchIterations; $i++) {
        $elapsed = $i * $WatchIntervalSec
        Write-Host ("--- t+{0}s ---" -f $elapsed) -ForegroundColor Cyan
        kube "get deploy -n $NS -o custom-columns=NAME:.metadata.name,REP:.spec.replicas,READY:.status.readyReplicas"
        kube "get events -n $NS --sort-by='.lastTimestamp' " 2>$null |
            Select-String "OptimizationDecision|Actuated" |
            Select-Object -Last 4
        if ($i -lt $WatchIterations) { Start-Sleep -Seconds $WatchIntervalSec }
    }

    show "Optional: cluster-agent log tail (last 40 lines, scale/tune/actuation)"
    kube "logs -n optipilot-system deploy/optipilot-cluster-agent --tail=40" |
        Select-String "optimization decision|actuation applied|scale_|tune"

    show "Stopping load generators"
    Stop-DemoLoadGenerators

    show "Cooldown - waiting 130s (policy scaleDown cooldown is often 120s), then snapshot"
    Start-Sleep -Seconds 130
    kube "get deploy -n $NS -o custom-columns=NAME:.metadata.name,REP:.spec.replicas,READY:.status.readyReplicas"
    kube "get events -n $NS --sort-by='.lastTimestamp'" | Select-String "OptimizationDecision|Actuated" | Select-Object -Last 6

    Ensure-PortForward8090
    show "REST API - recent decisions (if journal has entries)"
    try {
        $d = Invoke-RestMethod -Uri 'http://127.0.0.1:8090/api/v1/decisions?limit=6' -TimeoutSec 6
        @($d) | ForEach-Object {
            $row = $_
            $tgt = $null
            if ($row.selectedAction) { $tgt = $row.selectedAction.targetReplica }
            [PSCustomObject]@{
                service    = $row.service
                actionType = $row.actionType
                target     = $tgt
                dryRun     = $row.dryRun
            }
        } | Format-Table -AutoSize
    } catch {
        Write-Host "  (could not reach API on :8090 - start port-forward manually)" -ForegroundColor Yellow
    }

    Write-Host "`nDone. For the UI: npm run dev in ui/dashboard + browser http://localhost:5173" -ForegroundColor Green
}

function Run-TrafficDemo {
    param(
        [switch]$IncludeExtraTargets
    )
    Check-ClusterHealth
    Demo-TrafficAutoscale -IncludeExtraTargets:$IncludeExtraTargets
}

# ============================================================
# SECTION 4 — VERTICAL AUTO-SCALING (PROOF)
# ============================================================
function Demo-VerticalScaling {
    pause-section "SECTION 4: Vertical Auto-Scaling - Right-Sizing"

    show "Current resource requests after OptiPilot right-sizing"
    kube "get deploy -n $NS -o custom-columns='NAME:.metadata.name,REPLICAS:.spec.replicas,CPU_REQ:.spec.template.spec.containers[0].resources.requests.cpu,MEM_REQ:.spec.template.spec.containers[0].resources.requests.memory,CPU_LIM:.spec.template.spec.containers[0].resources.limits.cpu'"

    show "OptiPilot TUNE events - vertical scaling audit trail"
    kube "get events -n $NS --sort-by='.lastTimestamp' --field-selector reason=OptimizationDecision" | `
        Select-String "tune"

    show "All actuation events (both horizontal and vertical)"
    kube "get events -n $NS --sort-by='.lastTimestamp' --field-selector reason=Actuated"

    show "Deployment revision history (example deployment)"
    kube "rollout history deploy/$DemoHorizontalDeployment -n $NS" 2>$null

    show "BEFORE vs AFTER right-sizing - replace this table with your workload numbers after tuning runs"
    $table = @(
        '  Deployment  | CPU Before | CPU After | Mem Before | Mem After'
        '  ------------------------------------------------------------'
        '  (example)   | 100m       | 27m       | 256Mi      | actual usage'
    ) -join [Environment]::NewLine
    Write-Host $table -ForegroundColor Cyan
}

# ============================================================
# SECTION 5 — LIVE CONTROLLER DECISIONS STREAM
# ============================================================
function Demo-LiveDecisions {
    pause-section "SECTION 5: Live Controller Decision Stream"

    show "Controller pod status"
    kube "get pod -n optipilot-system"

    show "Recent optimization decisions from controller logs"
    Write-Host "Watch for: 'optimization decision', 'Actuated', 'SLOViolation'" -ForegroundColor Yellow
    kube "logs -n optipilot-system deploy/optipilot-cluster-agent --since=10m --tail=80" | `
        Select-String "optimization decision|Actuated|SLOViolation|scale|tune"

    show "Live stream - 20 seconds (auto-continues after)"
    $job = Start-Job -ScriptBlock {
        param($ctx)
        kubectl --context $ctx logs -n optipilot-system deploy/optipilot-cluster-agent -f --since=5s 2>&1
    } -ArgumentList $CTX
    $deadline = (Get-Date).AddSeconds(20)
    while ((Get-Date) -lt $deadline) {
        Receive-Job -Job $job | Select-String "optimization decision|Actuated|SLOViolation|scale|tune"
        Start-Sleep -Milliseconds 500
    }
    Stop-Job  -Job $job -ErrorAction SilentlyContinue
    Remove-Job -Job $job -Force -ErrorAction SilentlyContinue
    Write-Host "(stream ended - continuing to Section 6)" -ForegroundColor DarkGray
}

# ============================================================
# SECTION 6 — SLO COMPLIANCE STATUS
# ============================================================
function Demo-SLOStatus {
    pause-section "SECTION 6: SLO Compliance"

    show "All ServiceObjective statuses"
    kube "get serviceobjectives -A -o wide"

    $soEx = Get-FirstDemoServiceObjectiveName
    if ($soEx) {
        show "Describe first ServiceObjective in namespace"
        kube "describe serviceobjective $soEx -n $NS"
    }

    show "SLO violation events"
    kube "get events -n $NS --sort-by='.lastTimestamp' --field-selector reason=SLOViolation" | `
        Select-Object -Last 10
}

# ============================================================
# SECTION 7 — REST API + DECISION JOURNAL
# ============================================================
function Demo-RestAPI {
    pause-section "SECTION 7: Decision Journal REST API"

    # Ensure port-forward is alive
    $alive = try { (Invoke-WebRequest http://localhost:8090/api/v1/decisions -UseBasicParsing -TimeoutSec 3).StatusCode -eq 200 } catch { $false }
    if (-not $alive) {
        Write-Host "Starting port-forward to OptiPilot agent on 8090..." -ForegroundColor Yellow
        Start-Process -WindowStyle Hidden powershell "-Command kubectl --context $CTX port-forward -n optipilot-system svc/optipilot-cluster-agent 8090:8090"
        Start-Sleep -Seconds 4
    }

    show "All optimization decisions (Decision Journal REST API)"
    try {
        $decisions = (Invoke-RestMethod http://localhost:8090/api/v1/decisions -TimeoutSec 5)
        if ($decisions.Count -eq 0) {
            Write-Host "  (no decisions recorded yet in this session)" -ForegroundColor DarkGray
        } else {
            $decisions | Select-Object -First 8 namespace, service, action, reason, timestamp | Format-Table -AutoSize
        }
    } catch { Write-Host "  API error: $_" -ForegroundColor Red }

    show "Decision summary stats"
    try {
        Invoke-RestMethod http://localhost:8090/api/v1/decisions/summary -TimeoutSec 5 | ConvertTo-Json
    } catch { Write-Host "  API error: $_" -ForegroundColor Red }

    show "Filter: only scale_up decisions for $DemoServiceNameForAPI"
    try {
        $scaled = Invoke-RestMethod "http://localhost:8090/api/v1/decisions?namespace=$NS&service=$DemoServiceNameForAPI" -TimeoutSec 5
        $scaled | Where-Object { $_.action -eq 'scale_up' } | Select-Object -First 5 action, reason, timestamp | Format-Table -AutoSize
    } catch { Write-Host "  API error: $_" -ForegroundColor Red }
}

# ============================================================
# SECTION 8 — WHAT-IF SIMULATOR
# ============================================================
function Demo-WhatIf {
    pause-section "SECTION 8: What-If Simulator"

    # Ensure port-forward is alive
    $alive = try { (Invoke-WebRequest http://localhost:8090/api/v1/decisions -UseBasicParsing -TimeoutSec 3).StatusCode -eq 200 } catch { $false }
    if (-not $alive) {
        Write-Host "Starting port-forward to OptiPilot agent on 8090..." -ForegroundColor Yellow
        Start-Process -WindowStyle Hidden powershell "-Command kubectl --context $CTX port-forward -n optipilot-system svc/optipilot-cluster-agent 8090:8090"
        Start-Sleep -Seconds 4
    }

    show "Simulate: what happens to SLO if we run 1 vs 3 vs 5 replicas?"
    $body = @{
        namespace = $NS
        service   = $DemoServiceNameForAPI
        scenarios = @(
            @{ replicas = 1; cpu_request = 0.027; memory_request_gib = 0.4 }
            @{ replicas = 3; cpu_request = 0.027; memory_request_gib = 0.4 }
            @{ replicas = 5; cpu_request = 0.027; memory_request_gib = 0.4 }
        )
        duration_hours = 24
    } | ConvertTo-Json -Depth 4
    try {
        Invoke-RestMethod http://localhost:8090/api/v1/simulate -Method Post `
            -ContentType 'application/json' -Body $body -TimeoutSec 10 | ConvertTo-Json -Depth 4
    } catch { Write-Host "  /simulate returned: $($_.Exception.Response.StatusCode) - endpoint may not be implemented yet" -ForegroundColor Yellow }

    show "SLO-Cost curve: find the optimal replica count"
    $curve = @{
        namespace      = $NS
        service        = $DemoServiceNameForAPI
        replica_range  = @{ min = 1; max = 8 }
        duration_hours = 24
    } | ConvertTo-Json -Depth 3
    try {
        Invoke-RestMethod http://localhost:8090/api/v1/simulate/slo-cost-curve -Method Post `
            -ContentType 'application/json' -Body $curve -TimeoutSec 10 | ConvertTo-Json -Depth 4
    } catch { Write-Host "  /simulate/slo-cost-curve returned: $($_.Exception.Response.StatusCode) - endpoint may not be implemented yet" -ForegroundColor Yellow }
}

# ============================================================
# SECTION 9 — PROMETHEUS METRICS (what OptiPilot reads)
# ============================================================
function Demo-Prometheus {
    pause-section "SECTION 9: Prometheus Metrics (OptiPilot cluster metrics)"

    show "Starting Prometheus port-forward on localhost:9090"
    $pf = Start-Process -PassThru -WindowStyle Hidden powershell `
          "-Command kubectl --context $CTX port-forward -n monitoring svc/kube-prometheus-stack-prometheus 9090:9090"
    Start-Sleep -Seconds 3

    show "Querying availability ratio - what drives SLO decisions"
    $q = [System.Web.HttpUtility]::UrlEncode("kube_deployment_status_replicas_available{namespace=`"$NS`"} / kube_deployment_spec_replicas{namespace=`"$NS`"}")
    try {
        (Invoke-RestMethod "http://localhost:9090/api/v1/query?query=$q").data.result | `
            Select-Object @{n='deployment';e={$_.metric.deployment}}, @{n='availability';e={$_.value[1]}} | `
            Format-Table
    } catch {
        Write-Host "Open http://localhost:9090 in browser and run:" -ForegroundColor Yellow
        Write-Host "kube_deployment_status_replicas_available{namespace=`"$NS`"} / kube_deployment_spec_replicas{namespace=`"$NS`"}" -ForegroundColor Cyan
    }

    show "CPU usage per deployment (what drives vertical tuning)"
    Write-Host "Open http://localhost:9090 in browser and query:" -ForegroundColor Yellow
    Write-Host "  avg by(pod)(rate(container_cpu_usage_seconds_total{namespace=`"$NS`",container!=`"`"}[5m]))" -ForegroundColor Cyan
    Write-Host "  container_memory_working_set_bytes{namespace=`"$NS`",container!=`"`"}" -ForegroundColor Cyan

    Write-Host "`nPort-forward running to localhost:9090 - open browser now" -ForegroundColor Green
    Read-Host "Press ENTER when done with Prometheus demo"
    Stop-Process -Id $pf.Id -Force -ErrorAction SilentlyContinue
}

# ============================================================
# SECTION 10 — FULL DECISION TIMELINE (KILLER SLIDE)
# ============================================================
function Demo-Timeline {
    pause-section "SECTION 10: Full Autonomous Decision Timeline"

    show "Every decision OptiPilot made - the complete autonomous loop"
    kube "get events -n $NS --sort-by='.lastTimestamp' --field-selector reason=OptimizationDecision"

    $timeline = @(
        ''
        '  Reading this timeline from top to bottom:'
        '  1. scale_up  - SLO violated, OptiPilot scales OUT replicas'
        '  2. tune      - SLO healthy, OptiPilot right-sizes CPU/memory'
        '  3. scale_up  - workload spike, SLO violated again, scales OUT'
        '  4. tune      - stabilized again, right-sizes again'
        ''
        '  This is the AUTONOMOUS LOOP:'
        '  SLO violation  -  horizontal scale  -  SLO recovers  -  vertical tune'
        '  No human intervention. No thresholds. No HPA YAML.'
        '  Just: define what healthy means for your workload; OptiPilot handles the rest.'
    ) -join [Environment]::NewLine
    Write-Host $timeline -ForegroundColor Cyan
}


# ============================================================
# FULL DEMO — run all sections in order
# ============================================================
function Run-FullDemo {
    Check-ClusterHealth
    Demo-Architecture
    Demo-CRDs
    Demo-HorizontalScaling
    Demo-VerticalScaling
    Demo-LiveDecisions
    Demo-SLOStatus
    Demo-RestAPI
    Demo-WhatIf
    Demo-Prometheus
    Demo-Timeline

    Write-Host "`n" -NoNewline
    Write-Host ("=" * 60) -ForegroundColor Green
    Write-Host "  DEMO COMPLETE - All sections presented!" -ForegroundColor Green
    Write-Host ("=" * 60) -ForegroundColor Green
}

# ── Quick reference ──────────────────────────────────────────
$banner = @(
    ''
    'OptiPilot AI Demo Script loaded.'
    ''
    'Available functions:'
    '  Test-DockerHealth         detect and auto-recover WSL2 zombie'
    '  Ensure-DockerReady        guard -- call before any cluster work'
    '  Check-ClusterHealth       verify cluster is ready before demo'
    '  Suspend-Demo              STOP: pause cluster between sessions'
    '  Reset-Cluster             START: resume cluster, restore replicas'
    '  Deploy-NewImage           rebuild + load image into kind'
    '  Run-FullDemo              run entire demo in order'
    '  Demo-Architecture         section 1'
    '  Demo-CRDs                 section 2'
    '  Demo-HorizontalScaling    section 3  (SLO violation via scale-to-0)'
    '  Demo-TrafficAutoscale     section 3b (HTTP loadgen + auto-watch - easiest review)'
    '  Start-DemoLoadGenerators / Stop-DemoLoadGenerators  (traffic only)'
    '  Run-TrafficDemo           health check + Demo-TrafficAutoscale'
    '  Demo-VerticalScaling      section 4  (shows before/after)'
    '  Demo-LiveDecisions        section 5  (streaming logs)'
    '  Demo-SLOStatus            section 6'
    '  Demo-RestAPI              section 7  (kubectl exec approach)'
    '  Demo-WhatIf               section 8'
    '  Demo-Prometheus           section 9  (opens port-forward)'
    '  Demo-Timeline             section 10 (killer closing)'
    ''
) -join [Environment]::NewLine
Write-Host $banner -ForegroundColor White
