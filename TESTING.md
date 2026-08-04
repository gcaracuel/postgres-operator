# Integration Testing Guide

This guide walks through setting up a local Kind cluster, deploying the operator
from the latest master branch, and testing all CRDs.

## Prerequisites

- [Docker](https://docs.docker.com/get-docker/)
- [Kind](https://kind.sigs.k8s.io/docs/user/quick-start/#installation)
- [kubectl](https://kubernetes.io/docs/tasks/tools/)
- [Helm](https://helm.sh/docs/intro/install/)

---

## 1. Build the Docker Image from Master

```bash
cd /Users/guillermo/Projects/github/gcaracuel/postgres-operator/master
docker build --no-cache -t postgres-operator:master .
```

Take note of the image SHA:

```bash
docker images postgres-operator:master --no-trunc --format "{{.ID}}"
```

## 2. Create a Kind Cluster

```bash
cat <<EOF | kind create cluster --name postgres-operator --config=-
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
  - role: control-plane
    extraPortMappings:
      - containerPort: 30001
        hostPort: 5432
EOF
```

## 3. Load the Image into Kind

```bash
kind load docker-image postgres-operator:integration-test --name postgres-operator
```

## 4. Start a PostgreSQL Server in the Cluster

Create a namespace and deploy a test PostgreSQL instance:

```bash
kubectl create namespace operators

# Deploy a test PostgreSQL 16 server
kubectl -n operators apply -f - <<'EOF'
apiVersion: v1
kind: Secret
metadata:
  name: postgres-server
  namespace: operators
type: Opaque
stringData:
  POSTGRES_USER: postgres
  POSTGRES_PASSWORD: testpassword
  POSTGRES_DB: postgres
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: postgres-server
  namespace: operators
spec:
  replicas: 1
  selector:
    matchLabels:
      app: postgres-server
  template:
    metadata:
      labels:
        app: postgres-server
    spec:
      containers:
        - name: postgres
          image: postgres:16-alpine
          ports:
            - containerPort: 5432
          env:
            - name: POSTGRES_USER
              value: postgres
            - name: POSTGRES_PASSWORD
              value: testpassword
            - name: POSTGRES_DB
              value: postgres
          readinessProbe:
            exec:
              command: ["pg_isready", "-U", "postgres"]
            initialDelaySeconds: 5
            periodSeconds: 5
---
apiVersion: v1
kind: Service
metadata:
  name: postgres-server
  namespace: operators
spec:
  type: NodePort
  selector:
    app: postgres-server
  ports:
    - port: 5432
      targetPort: 5432
      nodePort: 30001
EOF

# Wait for PostgreSQL to be ready
kubectl -n operators wait --for=condition=ready pod -l app=postgres-server --timeout=120s
```

## 5. Create the Operator Secret

```bash
kubectl -n operators apply -f - <<'EOF'
apiVersion: v1
kind: Secret
metadata:
  name: ext-postgres-operator
  namespace: operators
type: Opaque
stringData:
  POSTGRES_HOST: postgres-server.operators.svc.cluster.local:5432
  POSTGRES_USER: postgres
  POSTGRES_PASS: testpassword
  POSTGRES_URI_ARGS: sslmode=disable
  POSTGRES_CLOUD_PROVIDER: ""
  POSTGRES_DEFAULT_DATABASE: postgres
EOF
```

## 6. Install the Helm Chart with the Local Image

```bash
helm upgrade --install -n operators ext-postgres-operator ./charts/ext-postgres-operator \
  --set image.repository=postgres-operator \
  --set image.tag=master \
  --set image.pullPolicy=Never \
  --set postgres.host=postgres-server.operators.svc.cluster.local:5432 \
  --set postgres.user=postgres \
  --set postgres.password=testpassword \
  --set postgres.uri_args=sslmode=disable \
  --set postgres.default_database=postgres
```

Verify the operator pod is running:

```bash
kubectl -n operators get pods -l app.kubernetes.io/name=ext-postgres-operator
kubectl -n operators logs -l app.kubernetes.io/name=ext-postgres-operator
```

## 7. Create Test Resources

### 7.1 Create Databases

```bash
# Database 1: app1 with schemas orders, inventory and extension pgcrypto
kubectl apply -f examples/01-postgres-db1.yaml

# Database 2: analytics with schema reports and extension fuzzystrmatch
kubectl apply -f examples/02-postgres-db2.yaml

# Wait for databases to be ready
kubectl wait --for=condition=ready --timeout=60s postgres/app-db-1
kubectl wait --for=condition=ready --timeout=60s postgres/app-db-2
```

### 7.2 Create Internal Users (with Secrets)

```bash
# Admin user (OWNER privileges on app-db-1)
kubectl apply -f examples/03-user-owner.yaml

# Writer user (WRITE privileges on app-db-1)
kubectl apply -f examples/04-user-writer.yaml

# Reader user (READ privileges on app-db-1)
kubectl apply -f examples/05-user-reader.yaml

# Check the generated secrets
kubectl get secrets -l app.kubernetes.io/instance=app1-admin -o yaml
```

### 7.3 Create External Users (no Secrets, fixed role names)

```bash
# IAM reader on app-db-1 (with rds_iam extra role)
kubectl apply -f examples/06-external-user-reader-app1.yaml

# IAM reader on analytics DB (with rds_iam extra role)
kubectl apply -f examples/06-external-user-reader-analytics.yaml

# Check external user status
kubectl get postgresexternaluser iam-app1-reader -o yaml
kubectl get postgresexternaluser iam-analytics-reader -o yaml
```

### 7.4 Create Analytics Admin User

```bash
# Admin user (OWNER privileges on app-db-2)
kubectl apply -f examples/07-user-owner-analytics.yaml
```

### 7.5 Same External Role on Multiple Databases

This tests that deleting one CR does not drop the role if it still has memberships from another CR.

```bash
# Grant iam-app1-reader READ on analytics DB too (same role, different DB)
kubectl apply -f examples/08-external-user-reader-app1-on-analytics.yaml

# Verify: iam-app1-reader now has two CRs granting different group roles
kubectl get postgresexternaluser -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.spec.roleName}{"\t"}{.spec.database}{"\t"}{.status.succeeded}{"\t"}{.status.message}{"\n"}{end}'
```

## 8. Verify Everything

### Check CR Status

```bash
# List all resources
kubectl get postgres
kubectl get postgresuser
kubectl get postgresexternaluser

# Check detailed status
kubectl get postgres app-db-1 -o jsonpath='{.status.message}'
kubectl get postgresuser app1-admin -o jsonpath='{.status.message}'
kubectl get postgresexternaluser iam-app1-reader -o jsonpath='{.status.message}'
```

### Verify Secrets (PostgresUser)

```bash
# Check that secrets contain POSTGRES_DSN
kubectl get secret app1-admin-app1-admin -o jsonpath='{.data.POSTGRES_DSN}' | base64 -d
echo
kubectl get secret app1-writer-app1-writer -o jsonpath='{.data.POSTGRES_DSN}' | base64 -d
echo
kubectl get secret app1-reader-app1-reader -o jsonpath='{.data.POSTGRES_DSN}' | base64 -d
```

### Connect to PostgreSQL and Verify

```bash
# Get the pod name
POD=$(kubectl -n operators get pod -l app=postgres-server -o name)

# Connect and list databases
kubectl -n operators exec $POD -- psql -U postgres -c "\l"

# List roles
kubectl -n operators exec $POD -- psql -U postgres -c "\du"

# Check app1 database schemas
kubectl -n operators exec $POD -- psql -U postgres -d app1 -c "\dn"

# Check analytics database schemas
kubectl -n operators exec $POD -- psql -U postgres -d analytics -c "\dn"
```

## 9. Test Deletion

### 9.1 Safe Deletion: Same Role on Multiple DBs

When the same `roleName` is used in two CRs (different databases), deleting one CR should:
- Revoke the group role that CR granted
- NOT drop the role (it still has the other CR's group role)
- NOT revoke `rds_iam` (managed by IAM flag)

```bash
# Delete one of the two CRs referencing iam-app1-reader
kubectl delete postgresexternaluser iam-app1-reader-on-analytics

# Verify: iam-app1-reader role still exists (other CR still grants app1-reader)
kubectl get postgresexternaluser iam-app1-reader

# Check in PostgreSQL that the role still exists
POD=$(kubectl -n operators get pod -l app=postgres-server -o name)
kubectl -n operators exec $POD -- psql -U postgres -c "\du iam-app1-reader"
```

### 9.2 Delete a PostgresUser

```bash
# Delete a user and verify the secret is cleaned up
kubectl delete postgresuser app1-reader
```

### 9.3 Delete the Last External User CR

When the last CR for a role is deleted, the role should be dropped (no other memberships).

```bash
# Delete the remaining CR for iam-app1-reader
kubectl delete postgresexternaluser iam-app1-reader

# Verify: role should be gone from PostgreSQL
POD=$(kubectl -n operators get pod -l app=postgres-server -o name)
kubectl -n operators exec $POD -- psql -U postgres -c "\du iam-app1-reader"
```

### 9.4 Delete a Database

```bash
# Delete a database (with dropOnDelete=false, roles and DB are preserved)
kubectl delete postgres app-db-2
```

## 10. Cleanup

```bash
# Delete the kind cluster
kind delete cluster --name postgres-operator
```

## Troubleshooting

### Operator logs

```bash
kubectl -n operators logs -l app.kubernetes.io/name=ext-postgres-operator -f
```

### PostgreSQL is not ready

```bash
kubectl -n operators logs -l app=postgres-server
kubectl -n operators describe pod -l app=postgres-server
```

### Helm install fails

Check the values are correct:

```bash
helm template ./charts/ext-postgres-operator \
  --set image.repository=postgres-operator \
  --set image.tag=master \
  --set image.pullPolicy=Never
```
