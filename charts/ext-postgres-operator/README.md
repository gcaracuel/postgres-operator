# ext-postgres-operator Helm Chart

This Helm chart deploys the External Postgres Operator, which provides a way to manage PostgreSQL databases and users in a Kubernetes environment.

## Installation

To install the chart, add the repository and use the `helm upgrade --install` command:

```bash
helm repo add ext-postgres-operator https://movetokube.github.io/postgres-operator/
helm upgrade --install -n operators ext-postgres-operator ext-postgres-operator/ext-postgres-operator
```

## Compatibility

**NOTE:** Helm chart version `>= 3.0.0` requires External Secret Operator version `>= 0.17.0`. Ensure that you are using the correct versions to avoid compatibility issues.

**NOTE:** Helm chart version `>= 2.0.0` is only compatible with the Postgres Operator version `2.0.0`. Ensure that you are using the correct versions to avoid compatibility issues.

## IRSA (IAM Roles for Service Accounts) on EKS

The operator supports IAM database authentication for Amazon RDS using IRSA. This allows the operator to connect to RDS without a static password by generating short-lived IAM authentication tokens.

### Prerequisites

1. **EKS cluster** with an OIDC provider configured for your cluster
2. **RDS instance** with IAM database authentication enabled
3. **IAM role** with a trust policy that allows the operator's service account to assume it
4. **Database user** in RDS mapped to the IAM role

### Step 1: Create an IAM role with trust policy

Create an IAM role with the following trust policy, replacing the values with your own:

```json
{
    "Version": "2012-10-17",
    "Statement": [
        {
            "Effect": "Allow",
            "Principal": {
                "Federated": "arn:aws:iam::ACCOUNT:oidc-provider/oidc.eks.REGION.amazonaws.com/id/CLUSTER_OIDC_ID"
            },
            "Action": "sts:AssumeRoleWithWebIdentity",
            "Condition": {
                "StringEquals": {
                    "oidc.eks.REGION.amazonaws.com/id/CLUSTER_OIDC_ID:sub": "system:serviceaccount:NAMESPACE:SERVICE_ACCOUNT_NAME"
                }
            }
        }
    ]
}
```

### Step 2: Attach IAM policy

Attach a policy that allows `rds-db:connect` for the specific database user:

```json
{
    "Version": "2012-10-17",
    "Statement": [
        {
            "Effect": "Allow",
            "Action": "rds-db:connect",
            "Resource": "arn:aws:rds-db:REGION:ACCOUNT:dbuser:DB_RESOURCE_ID/DB_USER"
        }
    ]
}
```

To find the `DB_RESOURCE_ID`:
```bash
aws rds describe-db-instances --db-instance-identifier mydb --query "DBInstances[0].DbiResourceId" --output text
```

### Step 3: Configure the RDS database user

Connect to your RDS instance and create a database user mapped to the IAM role:

```sql
CREATE USER "db_user" IDENTIFIED WITH AWSAuthentication;
GRANT rds_iam TO "db_user";
```

> **Note:** The database user name must match the IAM role's `rds-db:connect` resource name.

### Step 4: Enable IAM authentication on the RDS instance

Ensure IAM database authentication is enabled on your RDS instance. This can be done via the AWS Console, CLI, or Terraform:

```bash
aws rds modify-db-instance \
    --db-instance-identifier mydb \
    --enable-iam-database-authentication \
    --apply-immediately
```

### Step 5: Configure the Helm chart

```yaml
irsa:
  enabled: true
  roleArn: arn:aws:iam::ACCOUNT:role/my-operator-role
  region: eu-west-1

serviceAccount:
  annotations: {}
  # Optional: override the service account name
  # name: "my-custom-name"

postgres:
  host: mydb.xxxxxx.REGION.rds.amazonaws.com:5432
  user: db_user
  # password is not needed when irsa.enabled=true
  cloud_provider: AWS
  # default database to connect to
  default_database: postgres
```

### How it works

1. The Helm chart annotates the operator's service account with `eks.amazonaws.com/role-arn`, associating it with the IAM role
2. When the operator starts, it detects `POSTGRES_USE_IAM_AUTH=true` and `POSTGRES_CLOUD_PROVIDER=AWS`
3. The operator uses the AWS SDK to generate a 15-minute IAM authentication token via `rds.GenerateDBAuthToken`
4. The token is used as the password in the PostgreSQL connection string
5. Each temporary connection (for schema creation, extension management, etc.) gets a fresh token

### Troubleshooting

**"IAM auth token generation failed"**
- Verify the service account has the correct IRSA annotation
- Check the IAM role trust policy allows the service account
- Ensure the IAM policy allows `rds-db:connect` for the correct database user
- Verify the AWS region is correct

**"password authentication failed for user"**
- Ensure IAM database authentication is enabled on the RDS instance
- Verify the database user was created with `IDENTIFIED WITH AWSAuthentication`
- Check that the user has been granted the `rds_iam` role
