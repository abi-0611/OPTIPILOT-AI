#Requires -Version 5.1
<#
.SYNOPSIS
  Spawns many in-cluster curl loops against Boutique frontend to raise CPU and drive OptiPilot scale/tune.

.DESCRIPTION
  Use after Online Boutique is deployed (namespace default). Pair with examples/boutique-slos.yaml
  aggressive settings and optional: kubectl set env deploy/loadgenerator -n default USERS=50 RATE=5

  Usage:
    cd optipilot-ai
    . .\hack\boutique-heavy-load.ps1
    Start-BoutiqueHeavyLoad -ParallelPods 12
    # ... watch events / Run-TrafficDemo ...
    Stop-BoutiqueHeavyLoad
#>

$ErrorActionPreference = 'Continue'

if (-not $BoutiqueLoadCtx) { $BoutiqueLoadCtx = 'kind-optipilot-quickstart' }
if (-not $BoutiqueLoadNs) { $BoutiqueLoadNs = 'default' }
if (-not $BoutiqueLoadUrl) { $BoutiqueLoadUrl = 'http://frontend/' }

$script:BoutiqueLoadPrefix = 'boutique-hl'

function Start-BoutiqueHeavyLoad {
    param(
        [int]$ParallelPods = 8,
        [string]$Url = $BoutiqueLoadUrl
    )
    Stop-BoutiqueHeavyLoad -Quiet
    Start-Sleep -Seconds 1
    $inner = "while true; do curl -sS -o /dev/null `"$Url`" || true; done"
    Write-Host "Starting $ParallelPods pods ($script:BoutiqueLoadPrefix-*) -> $Url" -ForegroundColor Cyan
    for ($i = 1; $i -le $ParallelPods; $i++) {
        $name = "$script:BoutiqueLoadPrefix-$i"
        kubectl --context $BoutiqueLoadCtx run $name --restart=Never -n $BoutiqueLoadNs `
            --image=curlimages/curl:8.5.0 -- sh -c $inner 2>$null
    }
    Start-Sleep -Seconds 2
    Write-Host "Done. kubectl get pods -n $BoutiqueLoadNs | Select-String $script:BoutiqueLoadPrefix" -ForegroundColor Gray
}

function Stop-BoutiqueHeavyLoad {
    param([switch]$Quiet)
    $pods = kubectl --context $BoutiqueLoadCtx get pods -n $BoutiqueLoadNs -o name 2>$null | Select-String $script:BoutiqueLoadPrefix
    foreach ($p in @($pods)) {
        if (-not $p) { continue }
        $n = ($p -replace '^pod/', '')
        kubectl --context $BoutiqueLoadCtx delete pod $n -n $BoutiqueLoadNs --ignore-not-found=true --force --grace-period=0 2>$null | Out-Null
    }
    if (-not $Quiet) { Write-Host "Boutique heavy-load pods removed." -ForegroundColor Green }
}

Write-Host ""
Write-Host "boutique-heavy-load.ps1  Start-BoutiqueHeavyLoad [-ParallelPods 8]  Stop-BoutiqueHeavyLoad" -ForegroundColor White
Write-Host ""
