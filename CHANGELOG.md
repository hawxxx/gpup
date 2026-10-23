# Changelog

## [0.3.0](https://github.com/hawxxx/gpup/compare/v0.2.0...v0.3.0) (2026-10-23)


### Features

* **bench:** add open-loop request-rate sweeps, SLO goodput, and error classes ([88ca1ee](https://github.com/hawxxx/gpup/commit/88ca1eefe4ac5cbfe8da89aa084940570238b05f))
* **web:** show open-loop sweeps, goodput, and error classes; meet WCAG AA contrast ([88ca1ee](https://github.com/hawxxx/gpup/commit/88ca1eefe4ac5cbfe8da89aa084940570238b05f))


### Bug Fixes

* **engine:** scope credentials to the endpoint origin and aggregate exporter series across ranks ([88ca1ee](https://github.com/hawxxx/gpup/commit/88ca1eefe4ac5cbfe8da89aa084940570238b05f))
* **security:** refuse link-local dials, derive session cookies, and harden Helm pods ([88ca1ee](https://github.com/hawxxx/gpup/commit/88ca1eefe4ac5cbfe8da89aa084940570238b05f))


### Performance

* **app:** drop per-request chunk gaps from live snapshots ([88ca1ee](https://github.com/hawxxx/gpup/commit/88ca1eefe4ac5cbfe8da89aa084940570238b05f))

## [0.2.0](https://github.com/hawxxx/gpup/compare/v0.1.0...v0.2.0) (2026-10-21)


### Features

* add per-point benchmark warm-up ([1584624](https://github.com/hawxxx/gpup/commit/15846244f336f7c44064a5ceeaa4278ee837df09))
* add saved run listing, CSV/JSON export, and deletion ([1584624](https://github.com/hawxxx/gpup/commit/15846244f336f7c44064a5ceeaa4278ee837df09))
* export engine queue and KV-cache gauges in /metrics ([1584624](https://github.com/hawxxx/gpup/commit/15846244f336f7c44064a5ceeaa4278ee837df09))
* normalize SGLang, llama.cpp, and TGI exporter metrics ([1584624](https://github.com/hawxxx/gpup/commit/15846244f336f7c44064a5ceeaa4278ee837df09))
* report chunk-gap (ITL) percentiles for benchmark points ([1584624](https://github.com/hawxxx/gpup/commit/15846244f336f7c44064a5ceeaa4278ee837df09))
* show live benchmark progress and KV-cache history in the dashboard ([1584624](https://github.com/hawxxx/gpup/commit/15846244f336f7c44064a5ceeaa4278ee837df09))


### Bug Fixes

* count reasoning tokens toward TTFT and TPOT ([1584624](https://github.com/hawxxx/gpup/commit/15846244f336f7c44064a5ceeaa4278ee837df09))
* preserve engine messages for in-stream errors ([1584624](https://github.com/hawxxx/gpup/commit/15846244f336f7c44064a5ceeaa4278ee837df09))
* probe inference targets concurrently ([1584624](https://github.com/hawxxx/gpup/commit/15846244f336f7c44064a5ceeaa4278ee837df09))
* probe Triton's HTTP OpenAI port during discovery ([1584624](https://github.com/hawxxx/gpup/commit/15846244f336f7c44064a5ceeaa4278ee837df09))
* read throttle reasons in the nvidia-smi collector ([1584624](https://github.com/hawxxx/gpup/commit/15846244f336f7c44064a5ceeaa4278ee837df09))
* restore chart history after restart ([1584624](https://github.com/hawxxx/gpup/commit/15846244f336f7c44064a5ceeaa4278ee837df09))
* show newest requests first on the overview ([1584624](https://github.com/hawxxx/gpup/commit/15846244f336f7c44064a5ceeaa4278ee837df09))
