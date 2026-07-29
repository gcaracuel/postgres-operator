# ext-postgres-operator Helm Chart

> **Fork Notice:** This chart is for a fork of [movetokube/postgres-operator](https://github.com/movetokube/postgres-operator).

The Helm chart repository is still hosted by the original project at `https://movetokube.github.io/postgres-operator/`.

## Installation

To install the chart, add the repository and use the `helm upgrade --install` command:

```bash
helm repo add ext-postgres-operator https://movetokube.github.io/postgres-operator/
helm upgrade --install -n operators ext-postgres-operator ext-postgres-operator/ext-postgres-operator
```

## Compatibility

**NOTE:** Helm chart version `>= 3.0.0` requires External Secret Operator version `>= 0.17.0`. Ensure that you are using the correct versions to avoid compatibility issues.

**NOTE:** Helm chart version `>= 2.0.0` is only compatible with the Postgres Operator version `2.0.0`. Ensure that you are using the correct versions to avoid compatibility issues.
