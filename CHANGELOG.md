# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## Withdrawn releases

`v0.2.0` and `v0.2.1` were failed release attempts and carry no
artifacts. The `v0.2.0` tag stays valid for `go install`, but GitHub
permanently reserves a tag once its release has existed, so it can never
carry binaries. Everything intended for those versions ships in the next
release.

## [0.2.2](https://github.com/lknappich/syncctl/compare/v1.1.0...v0.2.2) (2026-08-07)


### ⚠ BREAKING CHANGES

* **security:** a config file containing plaintext secrets no longer loads; move them to environment variables or pass --allow-literal-secrets while migrating. /metrics now binds loopback; set metrics.addr explicitly to widen it.
* Prometheus metrics are renamed from geo_sync_* to syncctl_*. Existing alerts and dashboards must be updated:   geo_sync_pg_replay_lag_seconds       -> syncctl_pg_replay_lag_seconds   geo_sync_drift_total                 -> syncctl_drift_total   geo_sync_sync_duration_seconds       -> syncctl_sync_duration_seconds   geo_sync_last_sync_timestamp_seconds -> syncctl_last_sync_timestamp_seconds

### Features

* add project frontend under frontend/ ([65ff76a](https://github.com/lknappich/syncctl/commit/65ff76ad8f0884c657f1998e1b75c4f6da0064a2))
* bounded-parallel git fetch, SLA honest labels, consistency tolerance, SECURITY.md ([4c7a81e](https://github.com/lknappich/syncctl/commit/4c7a81efc89573ee2f1fa4533b1d375a09e8e835))
* **ci:** attest container image provenance ([8782575](https://github.com/lknappich/syncctl/commit/87825757ce2659be0ac6658264d213d25a179e98))
* **ci:** attest container image provenance ([c9328c9](https://github.com/lknappich/syncctl/commit/c9328c98dd54960e653e5f95ddf5ea5620704912)), closes [#86](https://github.com/lknappich/syncctl/issues/86)
* config schema with env-only secret expansion ([0df7f51](https://github.com/lknappich/syncctl/commit/0df7f51da4f9a74a4065cfa1e189515a53f828ab))
* doctor prerequisite checker, init wizard, testing guide ([c3c5ba9](https://github.com/lknappich/syncctl/commit/c3c5ba9414ccfe87a1b4682cb7e84e677dbfecd7))
* failover controller, role-swap, runbook generator, SLA reporter ([f40e317](https://github.com/lknappich/syncctl/commit/f40e317cd92d41e6ecccc6c94651354b208f11ea))
* geoctl CLI skeleton — version, config-validate, serve ([dc76953](https://github.com/lknappich/syncctl/commit/dc76953f50faa13f504655ba9f32832e8c8bb387))
* git fetch, fs storage, registry, and API validator reconcilers ([68a977e](https://github.com/lknappich/syncctl/commit/68a977e39d39bf087edc55433ef212f7cfbdca04))
* git rsync, S3 object storage, consistency sweep, dbkey, readonly ([c3fcb02](https://github.com/lknappich/syncctl/commit/c3fcb0247a099cfe9b12c2b788321ea25f548a9b))
* PostgreSQL streaming replication reconciler + pg setup ([5c6583a](https://github.com/lknappich/syncctl/commit/5c6583a88dfa9d83d28010c5276bffb082a4afe1))
* **security:** enforce the two controls deferred to 1.0 ([1896ff8](https://github.com/lknappich/syncctl/commit/1896ff840274e41f53105c270dfab1daf830c30b)), closes [#76](https://github.com/lknappich/syncctl/issues/76)
* version, logging, and metrics foundations ([05e7773](https://github.com/lknappich/syncctl/commit/05e7773f2e30331e6e286fbf6dc60a682c24d55c))
* webhook receiver with debounce + drift auto-repair ([9b59312](https://github.com/lknappich/syncctl/commit/9b5931276bac14138c41e3b0233856b8495827ba))


### Bug Fixes

* align Go toolchain to 1.24 across CI, Dockerfile, and docs ([58fd36b](https://github.com/lknappich/syncctl/commit/58fd36ba8904a410820f52765eb568ed987cb656))
* align golangci-lint v2 config and resolve lint findings ([adfa5ca](https://github.com/lknappich/syncctl/commit/adfa5ca67f94931f8655f64ed7ccfc5e81dac54a))
* bump Go to 1.25 to fix CI pipeline failures ([d201ced](https://github.com/lknappich/syncctl/commit/d201cedda36c0e376cba5765d3bf92ab6bf7a7f7))
* bump pgx to v5.9.2 for GO-2026-5004 SQL injection fix ([eee0648](https://github.com/lknappich/syncctl/commit/eee0648f23de1e9aa858d4439fe30f4cedfec38d))
* **ci:** create the tag for the draft release before building ([8839414](https://github.com/lknappich/syncctl/commit/8839414c747b8da3f887da72f890c5ba2d4b82f0))
* **ci:** create the tag for the draft release before building ([527c7bf](https://github.com/lknappich/syncctl/commit/527c7bfeca9077a9011a03482a80f9b17167888c))
* **ci:** grant attestations to the job that calls release.yml ([e540532](https://github.com/lknappich/syncctl/commit/e540532a57d7f00b07af25527a4c4c4c6f4a6faf))
* **ci:** grant attestations to the job that calls release.yml ([e806d3b](https://github.com/lknappich/syncctl/commit/e806d3bc2461998d37b55ccffd34febbc35f72e3))
* **ci:** have release-please create the release as a draft ([4e9309c](https://github.com/lknappich/syncctl/commit/4e9309c2b95e3e4918ba167c2724fc47951558a1))
* **ci:** have release-please create the release as a draft ([e16cd1b](https://github.com/lknappich/syncctl/commit/e16cd1b2b187122f93460ec85a339185de687b70))
* **ci:** install syft before goreleaser for SBOM generation ([44d1050](https://github.com/lknappich/syncctl/commit/44d1050473f3a74209a7b6df51de69fa9a88f412))
* **ci:** use docker-container buildx driver for attestations ([1754ea6](https://github.com/lknappich/syncctl/commit/1754ea631957fbf04a1527c270a91b29596867ec))
* **config:** enforce the env:"required" tag on secret fields ([09b40b2](https://github.com/lknappich/syncctl/commit/09b40b2957f6f07555cb6d7a87cc5e107a3d3401))
* **config:** enforce the env:"required" tag on secret fields ([4e17c24](https://github.com/lknappich/syncctl/commit/4e17c240911a8cdf4dcd077c20b92ff6666b5f22)), closes [#38](https://github.com/lknappich/syncctl/issues/38)
* **config:** stop rejecting configs that loaded before ([ca3d937](https://github.com/lknappich/syncctl/commit/ca3d9375b920286b6cb26a196768fae5795d7d30))
* **config:** stop rejecting configs that loaded before ([4c4ada6](https://github.com/lknappich/syncctl/commit/4c4ada656769b91a572c14b49235a183c91edb1c))
* **config:** validate ssh host-key policy and warn when it is weak ([0bfe929](https://github.com/lknappich/syncctl/commit/0bfe9293de680d1f240b80cceaa0523d94949306))
* **config:** validate ssh host-key policy and warn when it is weak ([f7c06b0](https://github.com/lknappich/syncctl/commit/f7c06b089473361318f00a092059471d53dc0e5e)), closes [#41](https://github.com/lknappich/syncctl/issues/41)
* correctness bugs, robustness improvements, and code hygiene ([695492a](https://github.com/lknappich/syncctl/commit/695492a7dcf760e312d01c8eec6a05b88758ccf6))
* **docker:** use goreleaser pre-built binaries in image ([f7b883f](https://github.com/lknappich/syncctl/commit/f7b883f34b568084dc3b4130004e8a14ccc82a60))
* **doctor:** actually read gitlab.rb in the db_key_base presence check ([0f6d8a4](https://github.com/lknappich/syncctl/commit/0f6d8a4fb5d33617e31b445fb298c3fe30274a8d))
* **doctor:** actually read gitlab.rb in the db_key_base presence check ([81ebbcc](https://github.com/lknappich/syncctl/commit/81ebbcc9a2f118d03dcfcd7b2eb6690727df35d0)), closes [#67](https://github.com/lknappich/syncctl/issues/67)
* enforce TLS on postgres connections and safely encode DSN credentials ([68afcb1](https://github.com/lknappich/syncctl/commit/68afcb1e341ae0be6b57abcb255fea71d4001d43))
* **failover:** correct the promotion and role-swap sequences ([4ce8241](https://github.com/lknappich/syncctl/commit/4ce8241cad9671c619d02952f378464dee49e720))
* **failover:** correct the promotion and role-swap sequences ([bce1230](https://github.com/lknappich/syncctl/commit/bce1230de71a5e551431b2f541187d3971044da9)), closes [#46](https://github.com/lknappich/syncctl/issues/46)
* **failover:** probe primary liveness so manual promotion can succeed ([9825560](https://github.com/lknappich/syncctl/commit/98255608f1ca82bdeb0405745c06c6117dc9d6ae))
* **failover:** probe primary liveness so manual promotion can succeed ([a0f891f](https://github.com/lknappich/syncctl/commit/a0f891fff27d4d38e6429cf819f76ba1d6a4f93d)), closes [#39](https://github.com/lknappich/syncctl/issues/39)
* **failover:** require an explicit secondary instead of picking the first ([4745644](https://github.com/lknappich/syncctl/commit/4745644046b2883efc87eca82797617b96485f72))
* **failover:** require an explicit secondary instead of picking the first ([57e9667](https://github.com/lknappich/syncctl/commit/57e9667d99b977aee762b5f63d600e55026d1293)), closes [#66](https://github.com/lknappich/syncctl/issues/66)
* **gitfetch:** correct hashed storage layout to documented SHA-256 form ([d705a4c](https://github.com/lknappich/syncctl/commit/d705a4c1e58f2392779cae16607a909bfbbe626a))
* golangci-lint v2 formatter config, make govulncheck non-blocking ([54d4683](https://github.com/lknappich/syncctl/commit/54d4683a8a9ce483d0159008cfdd03ee67b29356))
* harden webhook receiver and add HTTP server timeouts ([d2ee95a](https://github.com/lknappich/syncctl/commit/d2ee95af54425582bffd8aa998f9d07cd43b8906))
* **lint:** restore gosec coverage and fix the leak it was hiding ([0342884](https://github.com/lknappich/syncctl/commit/0342884c0a1a6172944fe3d7e32253f3503d6394))
* **lint:** restore gosec coverage and fix the leak it was hiding ([268a409](https://github.com/lknappich/syncctl/commit/268a4096463d9abcad168f087cbe5e4710199e84)), closes [#42](https://github.com/lknappich/syncctl/issues/42)
* **pgsetup:** keep the replication password off argv and out of dry-run output ([0d81c24](https://github.com/lknappich/syncctl/commit/0d81c24499340525e442e207ebee7c53f59a8b6b))
* **pgsetup:** keep the replication password off argv and out of dry-run output ([ba41b1b](https://github.com/lknappich/syncctl/commit/ba41b1bf8d7bedde11b811a4438a0d53f5e8cbb6)), closes [#37](https://github.com/lknappich/syncctl/issues/37)
* pin Go 1.25.11 for govulncheck, install golangci-lint v2 from source ([139dd0a](https://github.com/lknappich/syncctl/commit/139dd0a86116237255c6ecb27d2c6386e4a68311))
* **privacy:** bound what repository metadata reaches logs and metrics ([835d1e0](https://github.com/lknappich/syncctl/commit/835d1e0b5d9a3454d26ce048d52f19248dc76285))
* **privacy:** bound what repository metadata reaches logs and metrics ([03fb422](https://github.com/lknappich/syncctl/commit/03fb422a3f36d935c8440d09a3b08d6d7e052fbb)), closes [#74](https://github.com/lknappich/syncctl/issues/74) [#75](https://github.com/lknappich/syncctl/issues/75)
* **readonly,config:** stop describing write suppression as Maintenance Mode ([d0d1e47](https://github.com/lknappich/syncctl/commit/d0d1e479f5d3a0896e291df95f8951a48ada3e70))
* **reconcilers:** stop reporting unearned success and silent failure ([ffe9046](https://github.com/lknappich/syncctl/commit/ffe90466960e13eba8a758751f44433c0c8ccb1c))
* **reconcilers:** stop reporting unearned success and silent failure ([d62aa96](https://github.com/lknappich/syncctl/commit/d62aa961df4b88889c1ab4f6b548c30f819eeb3c)), closes [#48](https://github.com/lknappich/syncctl/issues/48)
* registry 401 skip, pg_basebackup slot flags, auto.conf editing ([09cfcf4](https://github.com/lknappich/syncctl/commit/09cfcf4d3a5adb3cb7067112ec955b3ca505c794))
* **registry,sla:** check the real registry, and report measured metrics ([96fc64f](https://github.com/lknappich/syncctl/commit/96fc64fa82b6ba99ce48102506ae6e78588eb5a7))
* **registry,sla:** check the real registry, and report measured metrics ([a5ff3e4](https://github.com/lknappich/syncctl/commit/a5ff3e460f2be103bd737668ed295ef65e00d771)), closes [#47](https://github.com/lknappich/syncctl/issues/47)
* **release:** stop a prerelease from claiming the :latest image tag ([a678a14](https://github.com/lknappich/syncctl/commit/a678a1463a5173568f84acc94f5e34003ac3b851))
* **release:** stop a prerelease from claiming the :latest image tag ([28bb81f](https://github.com/lknappich/syncctl/commit/28bb81fefdce7eae8b30812762b58d773521487e))
* resolve env placeholders after YAML parse to prevent injection ([a7c320f](https://github.com/lknappich/syncctl/commit/a7c320ff9be638bc8f961f660ed3a09fa43130d4))
* resolve errcheck warnings flagged by golangci-lint ([7230587](https://github.com/lknappich/syncctl/commit/723058750810a91ad3051179f286ba72ae6bb106))
* resolve remaining errcheck warnings for golangci-lint ([43b3f06](https://github.com/lknappich/syncctl/commit/43b3f06f43529b32b01e07b949794db701c3cba2))
* **security:** close transport gaps on webhook, metrics, and external_url ([bcafeff](https://github.com/lknappich/syncctl/commit/bcafefffbfe17e233aca9be75eaaabeb3d08a2d6))
* **security:** close transport gaps on webhook, metrics, and external_url ([ed12ce5](https://github.com/lknappich/syncctl/commit/ed12ce5cebedb7f4266ae22df530ed51f40a81d4)), closes [#44](https://github.com/lknappich/syncctl/issues/44)
* **security:** constrain the registry auth realm to configured hosts ([8ed4d3d](https://github.com/lknappich/syncctl/commit/8ed4d3d5d88ad9e3e64fbb53aa9a856fb6e31c65))
* **security:** constrain the registry auth realm to configured hosts ([3ed8c03](https://github.com/lknappich/syncctl/commit/3ed8c03a70f409fb432100492bdb00f7570a3558)), closes [#81](https://github.com/lknappich/syncctl/issues/81)
* **security:** shell-quote config values interpolated into remote commands ([34116bd](https://github.com/lknappich/syncctl/commit/34116bd4242846a245b0f4d365900ba1cabbacf4))
* **security:** shell-quote config values interpolated into remote commands ([3052ad2](https://github.com/lknappich/syncctl/commit/3052ad28f1748c531b721cf04e871cc61195fe0a)), closes [#40](https://github.com/lknappich/syncctl/issues/40)
* **security:** validate DB-sourced repository paths on the sweep path ([e346deb](https://github.com/lknappich/syncctl/commit/e346deb142e4d23bd7bdc8391ee27cbd1858eb0a))
* **security:** validate DB-sourced repository paths on the sweep path ([3b6cde4](https://github.com/lknappich/syncctl/commit/3b6cde4021451bf8b29b3795cd5cec882d162203)), closes [#65](https://github.com/lknappich/syncctl/issues/65)
* **sshexec:** parse ssh_host into destination and port ([3af3036](https://github.com/lknappich/syncctl/commit/3af30364ed7d8fb2c8733d3440dea9500e653fca))
* **sshexec:** parse ssh_host into destination and port ([d050277](https://github.com/lknappich/syncctl/commit/d0502775d016cd9d08da738de5abc98fbdf1faa8)), closes [#36](https://github.com/lknappich/syncctl/issues/36)
* **sync:** reconcile every secondary, not just the first ([d6e7b15](https://github.com/lknappich/syncctl/commit/d6e7b15851d4eb1324f7828135ad30d31a40e564))
* **sync:** reconcile every secondary, not just the first ([02322f8](https://github.com/lknappich/syncctl/commit/02322f88b56849e9c7e6455b69b89fee26790f0a)), closes [#45](https://github.com/lknappich/syncctl/issues/45)


### Refactoring

* centralize ssh execution and pin host keys ([b8ec212](https://github.com/lknappich/syncctl/commit/b8ec212db76ed451aa93f632b06ada6f8c6ad5c6))
* **consistency,pgsetup,autorepair:** inject runners for test coverage ([3510afd](https://github.com/lknappich/syncctl/commit/3510afd4b061cc36d2e61750761fd581da98a263))
* **doctor:** inject Runner and PoolFactory for full test coverage ([e8449fb](https://github.com/lknappich/syncctl/commit/e8449fb056360aac26b45188ee34b6012f2b795b))
* **gitrsync,fsstorage,gitfetch:** inject localcmd.Runner for tests ([d0e0c32](https://github.com/lknappich/syncctl/commit/d0e0c329e9e9df2e42fa5344ac93e7cb14cb14ca))
* **postgres,objectstorage:** extract Querier and bucketLister interfaces ([a544588](https://github.com/lknappich/syncctl/commit/a544588c96c93c87f38837e0b3aa3370953c72a2))
* rename project to syncctl ([872a4dc](https://github.com/lknappich/syncctl/commit/872a4dcd1436528dc81800bbc433de78ba30cb3c))
* **sshexec:** introduce Runner interface for mockable SSH calls ([ac24e55](https://github.com/lknappich/syncctl/commit/ac24e551246c1359c75e48b482d49514e0e1a201))
* unify module path to github.com/lknappich/gitlab-geo-sync ([63c1a77](https://github.com/lknappich/syncctl/commit/63c1a772e31392ed93cb8dd3a372af2975121ddc))


### Documentation

* add PRIVACY.md covering the data replication moves ([42e79aa](https://github.com/lknappich/syncctl/commit/42e79aabed8f73756d67341a59ecd7e62ef122ea))
* add PRIVACY.md covering the data replication moves ([bdfe7f4](https://github.com/lknappich/syncctl/commit/bdfe7f44976d6ceffb4b24d821577770bca59b4f)), closes [#73](https://github.com/lknappich/syncctl/issues/73)
* **agents,readme:** add trademark policy and disclaimer ([f901a7d](https://github.com/lknappich/syncctl/commit/f901a7d0f5ae9fef8224915288dbbb9defbcb43c))
* point vuln reports to GitHub private reporting ([bf57dd2](https://github.com/lknappich/syncctl/commit/bf57dd261fff9a9cdc0e5d933fe9cf443e2078fb))
* **privacy:** mark PRIVACY.md as not legal advice and hedge its claims ([329bdd2](https://github.com/lknappich/syncctl/commit/329bdd2bc5183fa6b0200f74a0eaa67b44f9f9eb))
* **privacy:** mark PRIVACY.md as not legal advice and hedge its claims ([fcac837](https://github.com/lknappich/syncctl/commit/fcac8375ed82c24dc5bd8637e09737ba0cbd842b))
* **privacy:** note the 1.0 loopback default for /metrics ([26095c9](https://github.com/lknappich/syncctl/commit/26095c9c2f4f5f9fdbac395ab399be06866bf552))
* **privacy:** note the 1.0 loopback default for /metrics ([81b9131](https://github.com/lknappich/syncctl/commit/81b91310fa9dd8c35c90b82b880e38f0684dd3c1))
* refresh README, add community files, golangci-lint config, tests ([85f7a80](https://github.com/lknappich/syncctl/commit/85f7a80d87b554b1bd465c685df2194c9278e711))
* **security:** say which releases carry image attestation ([ce256dd](https://github.com/lknappich/syncctl/commit/ce256ddc0794b1fb8dc7f48536b3e947084c2e67))
* **security:** say which releases carry image attestation ([70739d7](https://github.com/lknappich/syncctl/commit/70739d70da09130a885dbca9d81c8e4250977984))


### Chores

* reset release baseline to v0.1.0 and cut 0.2.2 ([2b0e0c0](https://github.com/lknappich/syncctl/commit/2b0e0c074f10e17386158825b5b9090fbe8c2034))

## [1.1.0](https://github.com/lknappich/syncctl/compare/v1.0.0...v1.1.0) (2026-08-06)


### Features

* **ci:** attest container image provenance ([8782575](https://github.com/lknappich/syncctl/commit/87825757ce2659be0ac6658264d213d25a179e98))
* **ci:** attest container image provenance ([c9328c9](https://github.com/lknappich/syncctl/commit/c9328c98dd54960e653e5f95ddf5ea5620704912)), closes [#86](https://github.com/lknappich/syncctl/issues/86)


### Documentation

* **security:** say which releases carry image attestation ([ce256dd](https://github.com/lknappich/syncctl/commit/ce256ddc0794b1fb8dc7f48536b3e947084c2e67))
* **security:** say which releases carry image attestation ([70739d7](https://github.com/lknappich/syncctl/commit/70739d70da09130a885dbca9d81c8e4250977984))

## [1.0.0](https://github.com/lknappich/syncctl/compare/v0.2.2...v1.0.0) (2026-08-06)


### ⚠ BREAKING CHANGES

* **security:** a config file containing plaintext secrets no longer loads; move them to environment variables or pass --allow-literal-secrets while migrating. /metrics now binds loopback; set metrics.addr explicitly to widen it.

### Features

* **security:** enforce the two controls deferred to 1.0 ([1896ff8](https://github.com/lknappich/syncctl/commit/1896ff840274e41f53105c270dfab1daf830c30b)), closes [#76](https://github.com/lknappich/syncctl/issues/76)


### Bug Fixes

* **ci:** grant attestations to the job that calls release.yml ([e540532](https://github.com/lknappich/syncctl/commit/e540532a57d7f00b07af25527a4c4c4c6f4a6faf))
* **ci:** grant attestations to the job that calls release.yml ([e806d3b](https://github.com/lknappich/syncctl/commit/e806d3bc2461998d37b55ccffd34febbc35f72e3))
* **config:** enforce the env:"required" tag on secret fields ([09b40b2](https://github.com/lknappich/syncctl/commit/09b40b2957f6f07555cb6d7a87cc5e107a3d3401))
* **config:** enforce the env:"required" tag on secret fields ([4e17c24](https://github.com/lknappich/syncctl/commit/4e17c240911a8cdf4dcd077c20b92ff6666b5f22)), closes [#38](https://github.com/lknappich/syncctl/issues/38)
* **config:** stop rejecting configs that loaded before ([ca3d937](https://github.com/lknappich/syncctl/commit/ca3d9375b920286b6cb26a196768fae5795d7d30))
* **config:** stop rejecting configs that loaded before ([4c4ada6](https://github.com/lknappich/syncctl/commit/4c4ada656769b91a572c14b49235a183c91edb1c))
* **config:** validate ssh host-key policy and warn when it is weak ([0bfe929](https://github.com/lknappich/syncctl/commit/0bfe9293de680d1f240b80cceaa0523d94949306))
* **config:** validate ssh host-key policy and warn when it is weak ([f7c06b0](https://github.com/lknappich/syncctl/commit/f7c06b089473361318f00a092059471d53dc0e5e)), closes [#41](https://github.com/lknappich/syncctl/issues/41)
* **doctor:** actually read gitlab.rb in the db_key_base presence check ([0f6d8a4](https://github.com/lknappich/syncctl/commit/0f6d8a4fb5d33617e31b445fb298c3fe30274a8d))
* **doctor:** actually read gitlab.rb in the db_key_base presence check ([81ebbcc](https://github.com/lknappich/syncctl/commit/81ebbcc9a2f118d03dcfcd7b2eb6690727df35d0)), closes [#67](https://github.com/lknappich/syncctl/issues/67)
* **failover:** correct the promotion and role-swap sequences ([4ce8241](https://github.com/lknappich/syncctl/commit/4ce8241cad9671c619d02952f378464dee49e720))
* **failover:** correct the promotion and role-swap sequences ([bce1230](https://github.com/lknappich/syncctl/commit/bce1230de71a5e551431b2f541187d3971044da9)), closes [#46](https://github.com/lknappich/syncctl/issues/46)
* **failover:** probe primary liveness so manual promotion can succeed ([9825560](https://github.com/lknappich/syncctl/commit/98255608f1ca82bdeb0405745c06c6117dc9d6ae))
* **failover:** probe primary liveness so manual promotion can succeed ([a0f891f](https://github.com/lknappich/syncctl/commit/a0f891fff27d4d38e6429cf819f76ba1d6a4f93d)), closes [#39](https://github.com/lknappich/syncctl/issues/39)
* **failover:** require an explicit secondary instead of picking the first ([4745644](https://github.com/lknappich/syncctl/commit/4745644046b2883efc87eca82797617b96485f72))
* **failover:** require an explicit secondary instead of picking the first ([57e9667](https://github.com/lknappich/syncctl/commit/57e9667d99b977aee762b5f63d600e55026d1293)), closes [#66](https://github.com/lknappich/syncctl/issues/66)
* **lint:** restore gosec coverage and fix the leak it was hiding ([0342884](https://github.com/lknappich/syncctl/commit/0342884c0a1a6172944fe3d7e32253f3503d6394))
* **lint:** restore gosec coverage and fix the leak it was hiding ([268a409](https://github.com/lknappich/syncctl/commit/268a4096463d9abcad168f087cbe5e4710199e84)), closes [#42](https://github.com/lknappich/syncctl/issues/42)
* **pgsetup:** keep the replication password off argv and out of dry-run output ([0d81c24](https://github.com/lknappich/syncctl/commit/0d81c24499340525e442e207ebee7c53f59a8b6b))
* **pgsetup:** keep the replication password off argv and out of dry-run output ([ba41b1b](https://github.com/lknappich/syncctl/commit/ba41b1bf8d7bedde11b811a4438a0d53f5e8cbb6)), closes [#37](https://github.com/lknappich/syncctl/issues/37)
* **privacy:** bound what repository metadata reaches logs and metrics ([835d1e0](https://github.com/lknappich/syncctl/commit/835d1e0b5d9a3454d26ce048d52f19248dc76285))
* **privacy:** bound what repository metadata reaches logs and metrics ([03fb422](https://github.com/lknappich/syncctl/commit/03fb422a3f36d935c8440d09a3b08d6d7e052fbb)), closes [#74](https://github.com/lknappich/syncctl/issues/74) [#75](https://github.com/lknappich/syncctl/issues/75)
* **reconcilers:** stop reporting unearned success and silent failure ([ffe9046](https://github.com/lknappich/syncctl/commit/ffe90466960e13eba8a758751f44433c0c8ccb1c))
* **reconcilers:** stop reporting unearned success and silent failure ([d62aa96](https://github.com/lknappich/syncctl/commit/d62aa961df4b88889c1ab4f6b548c30f819eeb3c)), closes [#48](https://github.com/lknappich/syncctl/issues/48)
* **registry,sla:** check the real registry, and report measured metrics ([96fc64f](https://github.com/lknappich/syncctl/commit/96fc64fa82b6ba99ce48102506ae6e78588eb5a7))
* **registry,sla:** check the real registry, and report measured metrics ([a5ff3e4](https://github.com/lknappich/syncctl/commit/a5ff3e460f2be103bd737668ed295ef65e00d771)), closes [#47](https://github.com/lknappich/syncctl/issues/47)
* **release:** stop a prerelease from claiming the :latest image tag ([a678a14](https://github.com/lknappich/syncctl/commit/a678a1463a5173568f84acc94f5e34003ac3b851))
* **release:** stop a prerelease from claiming the :latest image tag ([28bb81f](https://github.com/lknappich/syncctl/commit/28bb81fefdce7eae8b30812762b58d773521487e))
* **security:** close transport gaps on webhook, metrics, and external_url ([bcafeff](https://github.com/lknappich/syncctl/commit/bcafefffbfe17e233aca9be75eaaabeb3d08a2d6))
* **security:** close transport gaps on webhook, metrics, and external_url ([ed12ce5](https://github.com/lknappich/syncctl/commit/ed12ce5cebedb7f4266ae22df530ed51f40a81d4)), closes [#44](https://github.com/lknappich/syncctl/issues/44)
* **security:** constrain the registry auth realm to configured hosts ([8ed4d3d](https://github.com/lknappich/syncctl/commit/8ed4d3d5d88ad9e3e64fbb53aa9a856fb6e31c65))
* **security:** constrain the registry auth realm to configured hosts ([3ed8c03](https://github.com/lknappich/syncctl/commit/3ed8c03a70f409fb432100492bdb00f7570a3558)), closes [#81](https://github.com/lknappich/syncctl/issues/81)
* **security:** shell-quote config values interpolated into remote commands ([34116bd](https://github.com/lknappich/syncctl/commit/34116bd4242846a245b0f4d365900ba1cabbacf4))
* **security:** shell-quote config values interpolated into remote commands ([3052ad2](https://github.com/lknappich/syncctl/commit/3052ad28f1748c531b721cf04e871cc61195fe0a)), closes [#40](https://github.com/lknappich/syncctl/issues/40)
* **security:** validate DB-sourced repository paths on the sweep path ([e346deb](https://github.com/lknappich/syncctl/commit/e346deb142e4d23bd7bdc8391ee27cbd1858eb0a))
* **security:** validate DB-sourced repository paths on the sweep path ([3b6cde4](https://github.com/lknappich/syncctl/commit/3b6cde4021451bf8b29b3795cd5cec882d162203)), closes [#65](https://github.com/lknappich/syncctl/issues/65)
* **sshexec:** parse ssh_host into destination and port ([3af3036](https://github.com/lknappich/syncctl/commit/3af30364ed7d8fb2c8733d3440dea9500e653fca))
* **sshexec:** parse ssh_host into destination and port ([d050277](https://github.com/lknappich/syncctl/commit/d0502775d016cd9d08da738de5abc98fbdf1faa8)), closes [#36](https://github.com/lknappich/syncctl/issues/36)
* **sync:** reconcile every secondary, not just the first ([d6e7b15](https://github.com/lknappich/syncctl/commit/d6e7b15851d4eb1324f7828135ad30d31a40e564))
* **sync:** reconcile every secondary, not just the first ([02322f8](https://github.com/lknappich/syncctl/commit/02322f88b56849e9c7e6455b69b89fee26790f0a)), closes [#45](https://github.com/lknappich/syncctl/issues/45)


### Documentation

* add PRIVACY.md covering the data replication moves ([42e79aa](https://github.com/lknappich/syncctl/commit/42e79aabed8f73756d67341a59ecd7e62ef122ea))
* add PRIVACY.md covering the data replication moves ([bdfe7f4](https://github.com/lknappich/syncctl/commit/bdfe7f44976d6ceffb4b24d821577770bca59b4f)), closes [#73](https://github.com/lknappich/syncctl/issues/73)
* **privacy:** mark PRIVACY.md as not legal advice and hedge its claims ([329bdd2](https://github.com/lknappich/syncctl/commit/329bdd2bc5183fa6b0200f74a0eaa67b44f9f9eb))
* **privacy:** mark PRIVACY.md as not legal advice and hedge its claims ([fcac837](https://github.com/lknappich/syncctl/commit/fcac8375ed82c24dc5bd8637e09737ba0cbd842b))
* **privacy:** note the 1.0 loopback default for /metrics ([26095c9](https://github.com/lknappich/syncctl/commit/26095c9c2f4f5f9fdbac395ab399be06866bf552))
* **privacy:** note the 1.0 loopback default for /metrics ([81b9131](https://github.com/lknappich/syncctl/commit/81b91310fa9dd8c35c90b82b880e38f0684dd3c1))

## [0.2.2](https://github.com/lknappich/syncctl/compare/v0.2.2...v0.2.2) (2026-08-05)


### ⚠ BREAKING CHANGES

* Prometheus metrics are renamed from geo_sync_* to syncctl_*. Existing alerts and dashboards must be updated:   geo_sync_pg_replay_lag_seconds       -> syncctl_pg_replay_lag_seconds   geo_sync_drift_total                 -> syncctl_drift_total   geo_sync_sync_duration_seconds       -> syncctl_sync_duration_seconds   geo_sync_last_sync_timestamp_seconds -> syncctl_last_sync_timestamp_seconds

### Features

* add project frontend under frontend/ ([65ff76a](https://github.com/lknappich/syncctl/commit/65ff76ad8f0884c657f1998e1b75c4f6da0064a2))
* bounded-parallel git fetch, SLA honest labels, consistency tolerance, SECURITY.md ([4c7a81e](https://github.com/lknappich/syncctl/commit/4c7a81efc89573ee2f1fa4533b1d375a09e8e835))
* config schema with env-only secret expansion ([0df7f51](https://github.com/lknappich/syncctl/commit/0df7f51da4f9a74a4065cfa1e189515a53f828ab))
* doctor prerequisite checker, init wizard, testing guide ([c3c5ba9](https://github.com/lknappich/syncctl/commit/c3c5ba9414ccfe87a1b4682cb7e84e677dbfecd7))
* failover controller, role-swap, runbook generator, SLA reporter ([f40e317](https://github.com/lknappich/syncctl/commit/f40e317cd92d41e6ecccc6c94651354b208f11ea))
* geoctl CLI skeleton — version, config-validate, serve ([dc76953](https://github.com/lknappich/syncctl/commit/dc76953f50faa13f504655ba9f32832e8c8bb387))
* git fetch, fs storage, registry, and API validator reconcilers ([68a977e](https://github.com/lknappich/syncctl/commit/68a977e39d39bf087edc55433ef212f7cfbdca04))
* git rsync, S3 object storage, consistency sweep, dbkey, readonly ([c3fcb02](https://github.com/lknappich/syncctl/commit/c3fcb0247a099cfe9b12c2b788321ea25f548a9b))
* PostgreSQL streaming replication reconciler + pg setup ([5c6583a](https://github.com/lknappich/syncctl/commit/5c6583a88dfa9d83d28010c5276bffb082a4afe1))
* version, logging, and metrics foundations ([05e7773](https://github.com/lknappich/syncctl/commit/05e7773f2e30331e6e286fbf6dc60a682c24d55c))
* webhook receiver with debounce + drift auto-repair ([9b59312](https://github.com/lknappich/syncctl/commit/9b5931276bac14138c41e3b0233856b8495827ba))


### Bug Fixes

* align Go toolchain to 1.24 across CI, Dockerfile, and docs ([58fd36b](https://github.com/lknappich/syncctl/commit/58fd36ba8904a410820f52765eb568ed987cb656))
* align golangci-lint v2 config and resolve lint findings ([adfa5ca](https://github.com/lknappich/syncctl/commit/adfa5ca67f94931f8655f64ed7ccfc5e81dac54a))
* bump Go to 1.25 to fix CI pipeline failures ([d201ced](https://github.com/lknappich/syncctl/commit/d201cedda36c0e376cba5765d3bf92ab6bf7a7f7))
* bump pgx to v5.9.2 for GO-2026-5004 SQL injection fix ([eee0648](https://github.com/lknappich/syncctl/commit/eee0648f23de1e9aa858d4439fe30f4cedfec38d))
* **ci:** create the tag for the draft release before building ([8839414](https://github.com/lknappich/syncctl/commit/8839414c747b8da3f887da72f890c5ba2d4b82f0))
* **ci:** create the tag for the draft release before building ([527c7bf](https://github.com/lknappich/syncctl/commit/527c7bfeca9077a9011a03482a80f9b17167888c))
* **ci:** have release-please create the release as a draft ([4e9309c](https://github.com/lknappich/syncctl/commit/4e9309c2b95e3e4918ba167c2724fc47951558a1))
* **ci:** have release-please create the release as a draft ([e16cd1b](https://github.com/lknappich/syncctl/commit/e16cd1b2b187122f93460ec85a339185de687b70))
* **ci:** install syft before goreleaser for SBOM generation ([44d1050](https://github.com/lknappich/syncctl/commit/44d1050473f3a74209a7b6df51de69fa9a88f412))
* **ci:** use docker-container buildx driver for attestations ([1754ea6](https://github.com/lknappich/syncctl/commit/1754ea631957fbf04a1527c270a91b29596867ec))
* correctness bugs, robustness improvements, and code hygiene ([695492a](https://github.com/lknappich/syncctl/commit/695492a7dcf760e312d01c8eec6a05b88758ccf6))
* **docker:** use goreleaser pre-built binaries in image ([f7b883f](https://github.com/lknappich/syncctl/commit/f7b883f34b568084dc3b4130004e8a14ccc82a60))
* enforce TLS on postgres connections and safely encode DSN credentials ([68afcb1](https://github.com/lknappich/syncctl/commit/68afcb1e341ae0be6b57abcb255fea71d4001d43))
* **gitfetch:** correct hashed storage layout to documented SHA-256 form ([d705a4c](https://github.com/lknappich/syncctl/commit/d705a4c1e58f2392779cae16607a909bfbbe626a))
* golangci-lint v2 formatter config, make govulncheck non-blocking ([54d4683](https://github.com/lknappich/syncctl/commit/54d4683a8a9ce483d0159008cfdd03ee67b29356))
* harden webhook receiver and add HTTP server timeouts ([d2ee95a](https://github.com/lknappich/syncctl/commit/d2ee95af54425582bffd8aa998f9d07cd43b8906))
* pin Go 1.25.11 for govulncheck, install golangci-lint v2 from source ([139dd0a](https://github.com/lknappich/syncctl/commit/139dd0a86116237255c6ecb27d2c6386e4a68311))
* **readonly,config:** stop describing write suppression as Maintenance Mode ([d0d1e47](https://github.com/lknappich/syncctl/commit/d0d1e479f5d3a0896e291df95f8951a48ada3e70))
* registry 401 skip, pg_basebackup slot flags, auto.conf editing ([09cfcf4](https://github.com/lknappich/syncctl/commit/09cfcf4d3a5adb3cb7067112ec955b3ca505c794))
* resolve env placeholders after YAML parse to prevent injection ([a7c320f](https://github.com/lknappich/syncctl/commit/a7c320ff9be638bc8f961f660ed3a09fa43130d4))
* resolve errcheck warnings flagged by golangci-lint ([7230587](https://github.com/lknappich/syncctl/commit/723058750810a91ad3051179f286ba72ae6bb106))
* resolve remaining errcheck warnings for golangci-lint ([43b3f06](https://github.com/lknappich/syncctl/commit/43b3f06f43529b32b01e07b949794db701c3cba2))


### Refactoring

* centralize ssh execution and pin host keys ([b8ec212](https://github.com/lknappich/syncctl/commit/b8ec212db76ed451aa93f632b06ada6f8c6ad5c6))
* **consistency,pgsetup,autorepair:** inject runners for test coverage ([3510afd](https://github.com/lknappich/syncctl/commit/3510afd4b061cc36d2e61750761fd581da98a263))
* **doctor:** inject Runner and PoolFactory for full test coverage ([e8449fb](https://github.com/lknappich/syncctl/commit/e8449fb056360aac26b45188ee34b6012f2b795b))
* **gitrsync,fsstorage,gitfetch:** inject localcmd.Runner for tests ([d0e0c32](https://github.com/lknappich/syncctl/commit/d0e0c329e9e9df2e42fa5344ac93e7cb14cb14ca))
* **postgres,objectstorage:** extract Querier and bucketLister interfaces ([a544588](https://github.com/lknappich/syncctl/commit/a544588c96c93c87f38837e0b3aa3370953c72a2))
* rename project to syncctl ([872a4dc](https://github.com/lknappich/syncctl/commit/872a4dcd1436528dc81800bbc433de78ba30cb3c))
* **sshexec:** introduce Runner interface for mockable SSH calls ([ac24e55](https://github.com/lknappich/syncctl/commit/ac24e551246c1359c75e48b482d49514e0e1a201))
* unify module path to github.com/lknappich/gitlab-geo-sync ([63c1a77](https://github.com/lknappich/syncctl/commit/63c1a772e31392ed93cb8dd3a372af2975121ddc))


### Documentation

* **agents,readme:** add trademark policy and disclaimer ([f901a7d](https://github.com/lknappich/syncctl/commit/f901a7d0f5ae9fef8224915288dbbb9defbcb43c))
* point vuln reports to GitHub private reporting ([bf57dd2](https://github.com/lknappich/syncctl/commit/bf57dd261fff9a9cdc0e5d933fe9cf443e2078fb))
* refresh README, add community files, golangci-lint config, tests ([85f7a80](https://github.com/lknappich/syncctl/commit/85f7a80d87b554b1bd465c685df2194c9278e711))


### Chores

* reset release baseline to v0.1.0 and cut 0.2.2 ([2b0e0c0](https://github.com/lknappich/syncctl/commit/2b0e0c074f10e17386158825b5b9090fbe8c2034))

## [0.2.2](https://github.com/lknappich/syncctl/compare/v0.1.0...v0.2.2) (2026-08-05)


### ⚠ BREAKING CHANGES

* Prometheus metrics are renamed from geo_sync_* to syncctl_*. Existing alerts and dashboards must be updated:   geo_sync_pg_replay_lag_seconds       -> syncctl_pg_replay_lag_seconds   geo_sync_drift_total                 -> syncctl_drift_total   geo_sync_sync_duration_seconds       -> syncctl_sync_duration_seconds   geo_sync_last_sync_timestamp_seconds -> syncctl_last_sync_timestamp_seconds

### Bug Fixes

* **ci:** create the tag for the draft release before building ([8839414](https://github.com/lknappich/syncctl/commit/8839414c747b8da3f887da72f890c5ba2d4b82f0))
* **ci:** create the tag for the draft release before building ([527c7bf](https://github.com/lknappich/syncctl/commit/527c7bfeca9077a9011a03482a80f9b17167888c))
* **ci:** have release-please create the release as a draft ([4e9309c](https://github.com/lknappich/syncctl/commit/4e9309c2b95e3e4918ba167c2724fc47951558a1))
* **ci:** have release-please create the release as a draft ([e16cd1b](https://github.com/lknappich/syncctl/commit/e16cd1b2b187122f93460ec85a339185de687b70))
* **gitfetch:** correct hashed storage layout to documented SHA-256 form ([d705a4c](https://github.com/lknappich/syncctl/commit/d705a4c1e58f2392779cae16607a909bfbbe626a))
* **readonly,config:** stop describing write suppression as Maintenance Mode ([d0d1e47](https://github.com/lknappich/syncctl/commit/d0d1e479f5d3a0896e291df95f8951a48ada3e70))


### Refactoring

* **consistency,pgsetup,autorepair:** inject runners for test coverage ([3510afd](https://github.com/lknappich/syncctl/commit/3510afd4b061cc36d2e61750761fd581da98a263))
* **doctor:** inject Runner and PoolFactory for full test coverage ([e8449fb](https://github.com/lknappich/syncctl/commit/e8449fb056360aac26b45188ee34b6012f2b795b))
* **gitrsync,fsstorage,gitfetch:** inject localcmd.Runner for tests ([d0e0c32](https://github.com/lknappich/syncctl/commit/d0e0c329e9e9df2e42fa5344ac93e7cb14cb14ca))
* **postgres,objectstorage:** extract Querier and bucketLister interfaces ([a544588](https://github.com/lknappich/syncctl/commit/a544588c96c93c87f38837e0b3aa3370953c72a2))
* rename project to syncctl ([872a4dc](https://github.com/lknappich/syncctl/commit/872a4dcd1436528dc81800bbc433de78ba30cb3c))
* **sshexec:** introduce Runner interface for mockable SSH calls ([ac24e55](https://github.com/lknappich/syncctl/commit/ac24e551246c1359c75e48b482d49514e0e1a201))


### Documentation

* **agents,readme:** add trademark policy and disclaimer ([f901a7d](https://github.com/lknappich/syncctl/commit/f901a7d0f5ae9fef8224915288dbbb9defbcb43c))


### Chores

* reset release baseline to v0.1.0 and cut 0.2.2 ([2b0e0c0](https://github.com/lknappich/syncctl/commit/2b0e0c074f10e17386158825b5b9090fbe8c2034))

## [Unreleased]

### Fixed

- **PostgreSQL TLS**: Connections now default to `sslmode=require` instead of
  `sslmode=disable`. Passwords with special characters (spaces, quotes,
  backslashes) are safely encoded using libpq quoting rules.
- **Webhook security**: Empty secret token is now rejected at construction.
  Project paths are validated against path traversal (`../../`). Concurrency
  is capped (8 concurrent syncs) to prevent DoS. HTTP server timeouts prevent
  Slowloris attacks.
- **Metrics server**: Added ReadHeaderTimeout/ReadTimeout/WriteTimeout/IdleTimeout.
- **YAML env injection**: `${VAR}` placeholders are resolved after YAML parse
  via struct reflection, preventing injection of additional YAML keys via
  newline-containing env values.
- **Registry reconciler**: 401 Unauthorized is now treated as "skip" instead
  of reporting false drift.
- **pg_basebackup**: Removed duplicate `-S main` flag. Hardened
  `postgresql.auto.conf` editing to handle missing trailing newlines.
- **SSH host keys**: Centralized SSH execution in `internal/sshexec` package.
  `known_hosts_file` config field pins host keys with
  `StrictHostKeyChecking=yes`.
- **Failover safety**: Auto-failover logs a warning at config load time.
- **Consistency sweep**: 10% tolerance band on `reltuples` estimates prevents
  false drift from ANALYZE timing differences.

### Added

- **Bounded-parallel git fetch**: Worker pool (default 8, configurable via
  `SetMaxParallel`) materially improves sync time on large instances.
- **SECURITY.md**: Documents the sudo/SSH trust model, recommended sudoers
  allowlist, host-key verification, and db_key_base sharing rationale.
- **CODE_OF_CONDUCT.md**: Contributor Covenant 2.1.
- **CHANGELOG.md**: Keep-a-Changelog format.
- **Issue/PR templates**: GitHub issue templates and PR template.
- **Dependabot**: Weekly dependency updates for Go modules and GitHub Actions.
- **golangci-lint**: CI configuration with gosec, errcheck, govet, staticcheck,
  ineffassign, misspell, gofmt, revive.
- **Makefile**: `build`, `test`, `vet`, `lint`, `fmt`, `coverage`, `vuln`,
  `docker`, `release-snapshot` targets.
- **Dev config**: `deployments/dev/config.yaml` with `sslmode: disable` for
  local docker-compose stack.
- **docs/architecture.md**: Full architecture document.
- **Tests**: projectpath validation, sshexec config, config DSN encoding,
  pgsetup auto.conf editing, consistency tolerance.

### Changed

- Go toolchain aligned to 1.24 across `go.mod`, CI, Dockerfile, and docs.
- SLA report fields renamed from misleading `PGLagP50/PGLagP99` to honest
  `PGLagCurrent/PGLagPeak`. Component count derived dynamically from metrics.
- README updated from stale "Phase 0" status to reflect actual capabilities.
- CONTRIBUTING.md updated to reference Go 1.24.
