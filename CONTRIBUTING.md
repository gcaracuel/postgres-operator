# Contributing

This project is a fork of [movetokube/postgres-operator](https://github.com/movetokube/postgres-operator).
Contributions are welcome via pull requests.

## Branching

`master` branch contains the latest source code with all the features.

## Tests

Please write tests and fix any broken tests before you open a PR. Tests should cover at least 80% of your code.

## e2e-tests

End-to-end tests are implemented using [kuttl](https://kuttl.dev/), a Kubernetes test framework. To execute these tests locally, first install kuttl on your system, then run the command `make e2e` from the project root directory.
