# Changelog

## 1.0.0 (2026-07-10)


### Features

* Adding client-proxy and clickhouse to e2b-artifacts ([#3210](https://github.com/e2b-dev/infra/issues/3210)) ([5686d88](https://github.com/e2b-dev/infra/commit/5686d881e4c5c8a1712a5bd09a74b198172701b3))
* **api:** LD-gated ClickHouse read switcher ([#3061](https://github.com/e2b-dev/infra/issues/3061)) ([29e74ca](https://github.com/e2b-dev/infra/commit/29e74ca75aba785dedc5252957c750fa293fb036))
* **clickhouse:** implement multi-cluster fan-out for events and stats ([#2925](https://github.com/e2b-dev/infra/issues/2925)) ([39594c6](https://github.com/e2b-dev/infra/commit/39594c6eacba37a124ed5f2c8a8af95319c87ead))
* **migrations:** add webhook deliveries table to ClickHouse ([#2741](https://github.com/e2b-dev/infra/issues/2741)) ([f55a5bd](https://github.com/e2b-dev/infra/commit/f55a5bdd45804ad8866e9b8733ce73bb058eb820))
* **orchestrator:** LD-gated ClickHouse write fan-out feature flag ([#3152](https://github.com/e2b-dev/infra/issues/3152)) ([f046fcf](https://github.com/e2b-dev/infra/commit/f046fcf626a7e91f99c204507a8bf2ceed39e3e6))
* per-team events TTL limit (tier + addons) ([#3181](https://github.com/e2b-dev/infra/issues/3181)) ([f76b2cb](https://github.com/e2b-dev/infra/commit/f76b2cb622efde2e4958caa8358dfc95ba0b4ce7))


### Bug Fixes

* correct 3 CVES ([#3218](https://github.com/e2b-dev/infra/issues/3218)) ([076823b](https://github.com/e2b-dev/infra/commit/076823bc5cbffb9f8c04670c886562445e50ead7))
* push clickhouse-migrator image to both latest and commit SHA tags ([#2954](https://github.com/e2b-dev/infra/issues/2954)) ([3b780d5](https://github.com/e2b-dev/infra/commit/3b780d5d7620d022b1d8c5e4f79d0bc4b3c5ee81))
* reset artifacts ([#3259](https://github.com/e2b-dev/infra/issues/3259)) ([93f7eb5](https://github.com/e2b-dev/infra/commit/93f7eb57ce66fb72607bc0f3c1c40358a3c46c8a))
