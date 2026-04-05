# Guide: Multi-Cluster Setup

This tutorial configures a hub cluster receiving aggregated metrics and decisions from two spoke clusters, with mTLS secured gRPC communication.

**Time required:** ~45 minutes  
**Prerequisites:** 3 Kubernetes clusters, cert-manager installed, Helm 3.

---

## Architecture Overview

```
┌─────────────────────────────────────────────────┐
│  Hub Cluster (management plane)                 │
│  ┌─────────────┐  ┌──────────────────────────┐  │
│  │ OptiPilot   │  │ Aggregated Decision      │  │
│  │ Hub         │◄─┤ Journal & Metrics         │  │
│  │ :50051      │  └──────────────────────────┘  │
│  └──────┬──────┘                                │
│         │ mTLS gRPC                             │
└─────────┼───────────────────────────────────────┘
          │
   ┌──────┴──────────────────────────┐
   │                                  │
   ▼                                  ▼
┌──────────────────┐   ┌──────────────────┐
│ Spoke: prod-east │   │ Spoke: prod-west  │
│ cluster-agent    │   │ cluster-agent     │
│ running locally  │   │ running locally   │
└──────────────────┘   └──────────────────┘
```

---

## Step 1: Prepare Certificates

On the **hub cluster**, create a self-signed ClusterIssuer (or use an existing PKI):

```bash
# Hub cluster context
kubectl config use-context hub-cluster

kubectl apply -f - <<EOF
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: optipilot-ca
spec:
  selfSigned: {}
---
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: optipilot-ca
  namespace: optipilot-system
spec:
  isCA: true
  commonName: optipilot-ca
  secretName: optipilot-ca-secret
  issuerRef:
    name: optipilot-ca
    kind: ClusterIssuer
---
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: optipilot-issuer
spec:
  ca:
    secretName: optipilot-ca-secret
EOF
```

Export the CA certificate for spoke clusters:

```bash
kubectl get secret -n optipilot-system optipilot-ca-secret \
  -o jsonpath='{.data.ca\.crt}' | base64 -d > optipilot-ca.crt
```

---

## Step 2: Install Hub

```bash
helm install optipilot helm/optipilot \
  --namespace optipilot-system \
  --create-namespace \
  --set hub.enabled=true \
  --set hub.replicas=2 \
  --set clusterAgent.enabled=false \
  --set mtls.enabled=true \
  --set mtls.certManagerIssuer=optipilot-issuer
```

Get the hub's external address:

```bash
HUB_ADDR=$(kubectl get svc -n optipilot-system optipilot-hub-grpc \
  -o jsonpath='{.status.loadBalancer.ingress[0].hostname}')
echo "Hub address: $HUB_ADDR:50051"
```

---

## Step 3: Install Spoke: prod-east

```bash
kubectl config use-context prod-east

# Copy the CA cert as a secret
kubectl create namespace optipilot-system
kubectl create secret generic optipilot-ca \
  -n optipilot-system \
  --from-file=ca.crt=optipilot-ca.crt

helm install optipilot helm/optipilot \
  --namespace optipilot-system \
  --set clusterAgent.enabled=true \
  --set hub.enabled=false \
  --set mlService.enabled=false \
  --set clusterAgent.hubAddress="${HUB_ADDR}:50051" \
  --set clusterAgent.clusterName=prod-east \
  --set mtls.enabled=true \
  --set mtls.certManagerIssuer=optipilot-issuer
```

---

## Step 4: Install Spoke: prod-west

```bash
kubectl config use-context prod-west

kubectl create namespace optipilot-system
kubectl create secret generic optipilot-ca \
  -n optipilot-system \
  --from-file=ca.crt=optipilot-ca.crt

helm install optipilot helm/optipilot \
  --namespace optipilot-system \
  --set clusterAgent.enabled=true \
  --set hub.enabled=false \
  --set mlService.enabled=false \
  --set clusterAgent.hubAddress="${HUB_ADDR}:50051" \
  --set clusterAgent.clusterName=prod-west \
  --set mtls.enabled=true \
  --set mtls.certManagerIssuer=optipilot-issuer
```

---

## Step 5: Verify Connectivity

On the hub cluster, check that both spokes are registered:

```bash
kubectl config use-context hub-cluster

kubectl get events -n optipilot-system | grep ClusterRegistered
# Expected:
# Normal  ClusterRegistered  optipilot-hub  Cluster prod-east connected
# Normal  ClusterRegistered  optipilot-hub  Cluster prod-west connected
```

Check hub logs:

```bash
kubectl logs -n optipilot-system \
  -l app.kubernetes.io/name=optipilot-hub \
  --since=5m | grep registered
```

---

## Step 6: Global Decision Journal

All decisions from both spokes are forwarded to the hub's aggregated journal:

```bash
kubectl port-forward -n optipilot-system svc/optipilot-hub-grpc 8090:8090

curl -s 'http://localhost:8090/api/v1/decisions' | \
  jq '.decisions[] | {cluster: .clusterName, id, action}'
```

---

## Step 7: Cross-Cluster What-If

Simulate the same change across both clusters:

```bash
curl -X POST http://localhost:8090/api/v1/whatif/simulate \
  -d '{
    "clusters": ["prod-east", "prod-west"],
    "policyName": "global-policy",
    "scenario": {"replicas": 5, "spotEnabled": false}
  }' | jq '.'
```

---

## mTLS Certificate Rotation

Certificates are automatically rotated by cert-manager before expiry (configured by `mtls.certRenewBeforeHours`). To force immediate rotation:

```bash
kubectl delete certificate -n optipilot-system optipilot-cluster-agent-cert
# cert-manager will immediately issue a new one
```

---

## Troubleshooting Multi-Cluster

| Symptom | Cause | Fix |
|---|---|---|
| Spoke cannot connect to hub | Firewall blocking port 50051 | Open egress port 50051 from spoke to hub LB IP |
| `certificate verify failed` | CA cert mismatch | Ensure both clusters use the same CA from Step 1 |
| `cluster not registered` in journal | Hub address wrong on spoke | Check `clusterAgent.hubAddress` in spoke values |
| Duplicate decisions in journal | Hub restarted without persistent store | Enable `hub.persistence.enabled=true` |

---

## Next Steps

- [What-if simulation](what-if.md)
- [Tenant fairness across clusters](../crds/tenant-profile.md)
- [Configuration reference](../configuration.md)
