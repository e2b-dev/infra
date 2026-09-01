# Changelog

## 0.3.0 (2026-09-01)


### Features

* **api:** add guarded cluster cleanup (bb58376)
* **api:** remove deprecated E2B access token auth (a301352)
* **dashboard-api:** add authenticated cluster management operations (914ed3a)
* **dashboard-api:** add team access status endpoint (d95f364)
* **dashboard-api:** gate legacy team mutations (255470b)
* **db:** add PostgreSQL session primitives (22f0ccb)
* **db:** drop the access_tokens table (4c6dbff)
* **redis:** password auth and explicit TLS enablement (1c49ecf)
* register managed AWS clusters (8258bf0)
* **shared:** move RejectRoutes middleware to the shared package (deb735f)
* **shared:** use TypeID encoding for object IDs (4a5075b)


### Bug Fixes

* preserve default project memberships (aba2385)


### Dependencies

* bump go-redis to v9.21.0 across the OSS modules (1aa38f8)
* update Go to 1.26.6 (127e8a2)


### Code Refactoring

* **autobyoc:** rename managed service identity (a3e8683)


### Miscellaneous Chores

* **deps:** bump the go_modules group across 22 directories with 1 update (1b92ad2)

## [0.2.0](https://github.com/e2b-dev/infra/compare/dashboard-api-v0.1.0...dashboard-api-v0.2.0) (2026-08-17)


### Features

* add versioned project member projection ([#1467](https://github.com/e2b-dev/infra/issues/1467)) ([c09bfcd](https://github.com/e2b-dev/infra/commit/c09bfcdd300db7b7a56b9c9fa75316668833847b))
* add versioned project membership projection ([4c75786](https://github.com/e2b-dev/infra/commit/4c75786f26c8bf6375c00c8d775428b8447e7ca9))
* **dashboard-api:** fence pushed project limits with a revision ([#1732](https://github.com/e2b-dev/infra/issues/1732)) ([c6b4a1a](https://github.com/e2b-dev/infra/commit/c6b4a1aa7536f1a27e16fadba629608fb050dba1))
* **fcversion:** parse vX.Y-&lt;e2b-semver&gt; tags alongside {tag}_{sha} ([#1679](https://github.com/e2b-dev/infra/issues/1679)) ([a8cb6fb](https://github.com/e2b-dev/infra/commit/a8cb6fb8ce04d60fadfe566ef23bdc59b82c089d))


### Bug Fixes

* complete project member projection contract ([a9a327d](https://github.com/e2b-dev/infra/commit/a9a327d6fe3ed9924bf46c3c09098073ce6b5e7a))
* correct identity projection test ([ab12cee](https://github.com/e2b-dev/infra/commit/ab12cee5bbcdcb9bb91590725e02c9f5cb7835c1))
* **deps:** update module github.com/getkin/kin-openapi to v0.146.0 ([#1582](https://github.com/e2b-dev/infra/issues/1582)) ([5307ba8](https://github.com/e2b-dev/infra/commit/5307ba8613599106061c0b55f71be75718c2ad3c))
* **deps:** update module google.golang.org/grpc to v1.83.0 ([#1583](https://github.com/e2b-dev/infra/issues/1583)) ([acbefda](https://github.com/e2b-dev/infra/commit/acbefdae0e45d5c816cc8659dc8d34784ffa0056))
* **deps:** update opentelemetry ([#1700](https://github.com/e2b-dev/infra/issues/1700)) ([ea3100a](https://github.com/e2b-dev/infra/commit/ea3100a890ad7e2ba890b0112c41576589eb0e9d))
* **deps:** update opentelemetry-go-contrib monorepo ([#1607](https://github.com/e2b-dev/infra/issues/1607)) ([5852c94](https://github.com/e2b-dev/infra/commit/5852c942f9b5cff8a66ad008196d7025273febea))
* **deps:** update testcontainers-go monorepo to v0.43.0 ([#1701](https://github.com/e2b-dev/infra/issues/1701)) ([08b1cbe](https://github.com/e2b-dev/infra/commit/08b1cbe6b6e4e4cddf4b91c068e0b4632ce2e94c))
* harden project projection recovery ([c958d13](https://github.com/e2b-dev/infra/commit/c958d13ebc7d9b94f9d5c03ab6a4c353c291ae1d))
* preserve dashboard API generator path ([6c8ff94](https://github.com/e2b-dev/infra/commit/6c8ff9437eeb92842a6dd29cdbdc8ca14a7d2f6e))
* revoke access tokens on user deletion ([9127116](https://github.com/e2b-dev/infra/commit/912711613fbac71d42f8ead99e69da708e0a9b1e))

## [0.1.0](https://github.com/e2b-dev/infra/compare/dashboard-api-v0.0.1...dashboard-api-v0.1.0) (2026-07-31)


### Features

* add workspace admin API foundations ([#3314](https://github.com/e2b-dev/infra/issues/3314)) ([0f72030](https://github.com/e2b-dev/infra/commit/0f72030b3c54927d68b700b77281f544d567e7d6))
* **api:** LD-gated ClickHouse read switcher ([#3061](https://github.com/e2b-dev/infra/issues/3061)) ([29e74ca](https://github.com/e2b-dev/infra/commit/29e74ca75aba785dedc5252957c750fa293fb036))
* **api:** soft-delete build layers in DB on user delete ([#3121](https://github.com/e2b-dev/infra/issues/3121)) ([ee88776](https://github.com/e2b-dev/infra/commit/ee887768cf28e657f36aa7038495fad37859c144))
* **auth:** support admin token team auth ([#2934](https://github.com/e2b-dev/infra/issues/2934)) ([5496666](https://github.com/e2b-dev/infra/commit/549666684dedf3ce3418b2c1a58f0487cadb00ff))
* **auth:** verifiers on one axis, and a reusable authenticator constructor ([#3423](https://github.com/e2b-dev/infra/issues/3423)) ([923b99b](https://github.com/e2b-dev/infra/commit/923b99b134aaf71a294c0cfaa0bc7843dd54b50c))
* **dashboard-api:** add internal admin route for deleting a user ([#2986](https://github.com/e2b-dev/infra/issues/2986)) ([ecc1291](https://github.com/e2b-dev/infra/commit/ecc1291ad52bc35831735f704c0c88d3e0fbd2f7))
* **dashboard-api:** add internal team creation ([#2824](https://github.com/e2b-dev/infra/issues/2824)) ([375051b](https://github.com/e2b-dev/infra/commit/375051ba2a62503e84e9ed80319005736a8b67d2))
* **dashboard-api:** add OIDC admin user bootstrap endpoint ([#2841](https://github.com/e2b-dev/infra/issues/2841)) ([6a7a59e](https://github.com/e2b-dev/infra/commit/6a7a59ee031b7e3ef507679293fc5f5cb09c83b2))
* **dashboard-api:** add Ory user profile provider and auth middleware fix ([#2840](https://github.com/e2b-dev/infra/issues/2840)) ([30d40d2](https://github.com/e2b-dev/infra/commit/30d40d22fb3dcff8654d687f0c40c7e9639483a2))
* **dashboard-api:** add template tags handlers ([#2885](https://github.com/e2b-dev/infra/issues/2885)) ([bf52a4b](https://github.com/e2b-dev/infra/commit/bf52a4b7fc9c78c6a3fb782d34406dd31572b37a))
* **dashboard-api:** batch member sync route, and unenumerate project_type ([#3427](https://github.com/e2b-dev/infra/issues/3427)) ([cc16acf](https://github.com/e2b-dev/infra/commit/cc16acfc3b8c02347c10869b45fef8d75b9b8e8f))
* **dashboard-api:** expose auth profile admin routes ([#2743](https://github.com/e2b-dev/infra/issues/2743)) ([b673a10](https://github.com/e2b-dev/infra/commit/b673a10cbbdf82d7449267dc8e499b224b93821b))
* **dashboard-api:** flag sandboxes past data retention ([#3102](https://github.com/e2b-dev/infra/issues/3102)) ([9b162bf](https://github.com/e2b-dev/infra/commit/9b162bf9a4cba0d4459384d48bae67034959e275))
* **dashboard-api:** implement upsertProjectLimits ([#3438](https://github.com/e2b-dev/infra/issues/3438)) ([ec1ed29](https://github.com/e2b-dev/infra/commit/ec1ed29d6e75b3fa4061a508bb50d93ef715dc58))
* **dashboard-api:** include build resources in /builds response ([#3009](https://github.com/e2b-dev/infra/issues/3009)) ([bf49c32](https://github.com/e2b-dev/infra/commit/bf49c32454c1aa45a3461556fd4fdff60d48de13))
* **dashboard-api:** map Ory SSO organizations to E2B teams ([#3094](https://github.com/e2b-dev/infra/issues/3094)) ([dbd098f](https://github.com/e2b-dev/infra/commit/dbd098f9ff026956a85eb0efb099eb41c7552911))
* **dashboard-api:** populate Ory identity external_id on admin bootstrap ([#3062](https://github.com/e2b-dev/infra/issues/3062)) ([6c51232](https://github.com/e2b-dev/infra/commit/6c512329de91c3e8a3b49be8d9f72e61d794fcee))
* **dashboard-api:** project upsert, member sync and user purge ([#3442](https://github.com/e2b-dev/infra/issues/3442)) ([f997c39](https://github.com/e2b-dev/infra/commit/f997c39af87c53c8bfe9470a532e59455fd19200))
* **dashboard-api:** templates list pagination ([#2904](https://github.com/e2b-dev/infra/issues/2904)) ([6882463](https://github.com/e2b-dev/infra/commit/68824637b1010b195990b7aa5c0d686d77dce77e))
* **db:** add project_limits, an override the limits owner can write ([#3429](https://github.com/e2b-dev/infra/issues/3429)) ([021c2a4](https://github.com/e2b-dev/infra/commit/021c2a42a0f43068ed7e39880bb73c9d898e34e9))
* improve templates list sorting ([#2983](https://github.com/e2b-dev/infra/issues/2983)) ([51ad7ff](https://github.com/e2b-dev/infra/commit/51ad7ff04fa407babe2239c2b11607a499bf365e))
* **otel:** instrument auth service HTTP client with otelhttp ([#2722](https://github.com/e2b-dev/infra/issues/2722)) ([69b085d](https://github.com/e2b-dev/infra/commit/69b085d2f7e045efa03117e220d3fc970075c3df))
* per-team events TTL limit (tier + addons) ([#3181](https://github.com/e2b-dev/infra/issues/3181)) ([f76b2cb](https://github.com/e2b-dev/infra/commit/f76b2cb622efde2e4958caa8358dfc95ba0b4ce7))


### Bug Fixes

* added api and orch ([#3454](https://github.com/e2b-dev/infra/issues/3454)) ([fda5e45](https://github.com/e2b-dev/infra/commit/fda5e4579c59729cb8510dd47965c5192813ac8b))
* **api:** copy auth/internal into api and dashboard-api image builds ([#3323](https://github.com/e2b-dev/infra/issues/3323)) ([bda1fee](https://github.com/e2b-dev/infra/commit/bda1fee3a288a26beb91a06a469dc70730386319))
* **api:** invalidate auth cache on API key deletion ([#3324](https://github.com/e2b-dev/infra/issues/3324)) ([8b02910](https://github.com/e2b-dev/infra/commit/8b029108271d21273fab388146b9fe6b9fc547e8))
* correct 3 CVES ([#3218](https://github.com/e2b-dev/infra/issues/3218)) ([076823b](https://github.com/e2b-dev/infra/commit/076823bc5cbffb9f8c04670c886562445e50ead7))
* creating whitespace to test publish ([#3476](https://github.com/e2b-dev/infra/issues/3476)) ([5158cc9](https://github.com/e2b-dev/infra/commit/5158cc953c39a92851d71062d49e94eac36c539b))
* **dashboard-api:** avoid repeated Ory bootstrap provisioning ([#2940](https://github.com/e2b-dev/infra/issues/2940)) ([da5ce59](https://github.com/e2b-dev/infra/commit/da5ce59c8b81f78f071653b0c43f6fb1732488e0))
* **dashboard-api:** drop removed read-replica accessor in provisioning tests ([#3340](https://github.com/e2b-dev/infra/issues/3340)) ([6addc91](https://github.com/e2b-dev/infra/commit/6addc91b98462f9ab47ade57dc882cd5bc90c902))
* **dashboard-api:** pass signup metadata to billing provisioning ([#2978](https://github.com/e2b-dev/infra/issues/2978)) ([d0ea5b4](https://github.com/e2b-dev/infra/commit/d0ea5b421a25cf0064a84a4da350b27b6400061f))
* **dashboard-api:** set Ory external_id only after the bootstrap commit ([#3133](https://github.com/e2b-dev/infra/issues/3133)) ([00ad04b](https://github.com/e2b-dev/infra/commit/00ad04b13d60e97e1908829aa9cba77952517d77))
* push client-proxy, dashboard-api, and docker-reverse-proxy image… ([#2953](https://github.com/e2b-dev/infra/issues/2953)) ([1d930ee](https://github.com/e2b-dev/infra/commit/1d930ee60fd74b3ad1d5c167165b1005baa471fe))

## 0.0.1 (2026-07-30)


### Features

* add workspace admin API foundations ([#3314](https://github.com/e2b-dev/infra/issues/3314)) ([0f72030](https://github.com/e2b-dev/infra/commit/0f72030b3c54927d68b700b77281f544d567e7d6))
* **api:** LD-gated ClickHouse read switcher ([#3061](https://github.com/e2b-dev/infra/issues/3061)) ([29e74ca](https://github.com/e2b-dev/infra/commit/29e74ca75aba785dedc5252957c750fa293fb036))
* **api:** soft-delete build layers in DB on user delete ([#3121](https://github.com/e2b-dev/infra/issues/3121)) ([ee88776](https://github.com/e2b-dev/infra/commit/ee887768cf28e657f36aa7038495fad37859c144))
* **auth:** support admin token team auth ([#2934](https://github.com/e2b-dev/infra/issues/2934)) ([5496666](https://github.com/e2b-dev/infra/commit/549666684dedf3ce3418b2c1a58f0487cadb00ff))
* **auth:** verifiers on one axis, and a reusable authenticator constructor ([#3423](https://github.com/e2b-dev/infra/issues/3423)) ([923b99b](https://github.com/e2b-dev/infra/commit/923b99b134aaf71a294c0cfaa0bc7843dd54b50c))
* **dashboard-api:** add internal admin route for deleting a user ([#2986](https://github.com/e2b-dev/infra/issues/2986)) ([ecc1291](https://github.com/e2b-dev/infra/commit/ecc1291ad52bc35831735f704c0c88d3e0fbd2f7))
* **dashboard-api:** add internal team creation ([#2824](https://github.com/e2b-dev/infra/issues/2824)) ([375051b](https://github.com/e2b-dev/infra/commit/375051ba2a62503e84e9ed80319005736a8b67d2))
* **dashboard-api:** add OIDC admin user bootstrap endpoint ([#2841](https://github.com/e2b-dev/infra/issues/2841)) ([6a7a59e](https://github.com/e2b-dev/infra/commit/6a7a59ee031b7e3ef507679293fc5f5cb09c83b2))
* **dashboard-api:** add Ory user profile provider and auth middleware fix ([#2840](https://github.com/e2b-dev/infra/issues/2840)) ([30d40d2](https://github.com/e2b-dev/infra/commit/30d40d22fb3dcff8654d687f0c40c7e9639483a2))
* **dashboard-api:** add template tags handlers ([#2885](https://github.com/e2b-dev/infra/issues/2885)) ([bf52a4b](https://github.com/e2b-dev/infra/commit/bf52a4b7fc9c78c6a3fb782d34406dd31572b37a))
* **dashboard-api:** batch member sync route, and unenumerate project_type ([#3427](https://github.com/e2b-dev/infra/issues/3427)) ([cc16acf](https://github.com/e2b-dev/infra/commit/cc16acfc3b8c02347c10869b45fef8d75b9b8e8f))
* **dashboard-api:** expose auth profile admin routes ([#2743](https://github.com/e2b-dev/infra/issues/2743)) ([b673a10](https://github.com/e2b-dev/infra/commit/b673a10cbbdf82d7449267dc8e499b224b93821b))
* **dashboard-api:** flag sandboxes past data retention ([#3102](https://github.com/e2b-dev/infra/issues/3102)) ([9b162bf](https://github.com/e2b-dev/infra/commit/9b162bf9a4cba0d4459384d48bae67034959e275))
* **dashboard-api:** implement upsertProjectLimits ([#3438](https://github.com/e2b-dev/infra/issues/3438)) ([ec1ed29](https://github.com/e2b-dev/infra/commit/ec1ed29d6e75b3fa4061a508bb50d93ef715dc58))
* **dashboard-api:** include build resources in /builds response ([#3009](https://github.com/e2b-dev/infra/issues/3009)) ([bf49c32](https://github.com/e2b-dev/infra/commit/bf49c32454c1aa45a3461556fd4fdff60d48de13))
* **dashboard-api:** map Ory SSO organizations to E2B teams ([#3094](https://github.com/e2b-dev/infra/issues/3094)) ([dbd098f](https://github.com/e2b-dev/infra/commit/dbd098f9ff026956a85eb0efb099eb41c7552911))
* **dashboard-api:** populate Ory identity external_id on admin bootstrap ([#3062](https://github.com/e2b-dev/infra/issues/3062)) ([6c51232](https://github.com/e2b-dev/infra/commit/6c512329de91c3e8a3b49be8d9f72e61d794fcee))
* **dashboard-api:** project upsert, member sync and user purge ([#3442](https://github.com/e2b-dev/infra/issues/3442)) ([f997c39](https://github.com/e2b-dev/infra/commit/f997c39af87c53c8bfe9470a532e59455fd19200))
* **dashboard-api:** templates list pagination ([#2904](https://github.com/e2b-dev/infra/issues/2904)) ([6882463](https://github.com/e2b-dev/infra/commit/68824637b1010b195990b7aa5c0d686d77dce77e))
* **db:** add project_limits, an override the limits owner can write ([#3429](https://github.com/e2b-dev/infra/issues/3429)) ([021c2a4](https://github.com/e2b-dev/infra/commit/021c2a42a0f43068ed7e39880bb73c9d898e34e9))
* improve templates list sorting ([#2983](https://github.com/e2b-dev/infra/issues/2983)) ([51ad7ff](https://github.com/e2b-dev/infra/commit/51ad7ff04fa407babe2239c2b11607a499bf365e))
* **otel:** instrument auth service HTTP client with otelhttp ([#2722](https://github.com/e2b-dev/infra/issues/2722)) ([69b085d](https://github.com/e2b-dev/infra/commit/69b085d2f7e045efa03117e220d3fc970075c3df))
* per-team events TTL limit (tier + addons) ([#3181](https://github.com/e2b-dev/infra/issues/3181)) ([f76b2cb](https://github.com/e2b-dev/infra/commit/f76b2cb622efde2e4958caa8358dfc95ba0b4ce7))


### Bug Fixes

* added api and orch ([#3454](https://github.com/e2b-dev/infra/issues/3454)) ([fda5e45](https://github.com/e2b-dev/infra/commit/fda5e4579c59729cb8510dd47965c5192813ac8b))
* **api:** copy auth/internal into api and dashboard-api image builds ([#3323](https://github.com/e2b-dev/infra/issues/3323)) ([bda1fee](https://github.com/e2b-dev/infra/commit/bda1fee3a288a26beb91a06a469dc70730386319))
* **api:** invalidate auth cache on API key deletion ([#3324](https://github.com/e2b-dev/infra/issues/3324)) ([8b02910](https://github.com/e2b-dev/infra/commit/8b029108271d21273fab388146b9fe6b9fc547e8))
* correct 3 CVES ([#3218](https://github.com/e2b-dev/infra/issues/3218)) ([076823b](https://github.com/e2b-dev/infra/commit/076823bc5cbffb9f8c04670c886562445e50ead7))
* **dashboard-api:** avoid repeated Ory bootstrap provisioning ([#2940](https://github.com/e2b-dev/infra/issues/2940)) ([da5ce59](https://github.com/e2b-dev/infra/commit/da5ce59c8b81f78f071653b0c43f6fb1732488e0))
* **dashboard-api:** drop removed read-replica accessor in provisioning tests ([#3340](https://github.com/e2b-dev/infra/issues/3340)) ([6addc91](https://github.com/e2b-dev/infra/commit/6addc91b98462f9ab47ade57dc882cd5bc90c902))
* **dashboard-api:** pass signup metadata to billing provisioning ([#2978](https://github.com/e2b-dev/infra/issues/2978)) ([d0ea5b4](https://github.com/e2b-dev/infra/commit/d0ea5b421a25cf0064a84a4da350b27b6400061f))
* **dashboard-api:** set Ory external_id only after the bootstrap commit ([#3133](https://github.com/e2b-dev/infra/issues/3133)) ([00ad04b](https://github.com/e2b-dev/infra/commit/00ad04b13d60e97e1908829aa9cba77952517d77))
* push client-proxy, dashboard-api, and docker-reverse-proxy image… ([#2953](https://github.com/e2b-dev/infra/issues/2953)) ([1d930ee](https://github.com/e2b-dev/infra/commit/1d930ee60fd74b3ad1d5c167165b1005baa471fe))
