# Runtime contract

All numeric timings are milliseconds; timestamps are RFC3339. Unknown optional numeric fields serialize as null or are omitted. Arrays serialize as arrays, not null.

`GET /api/v1/snapshot`: `{time, mode: "live"|"demo", targets: Target[], models: Model[], gpus: GPU[], requests: RequestResult[], runs: Run[], findings: Finding[], history: SnapshotPoint[], collectorErrors: string[], metrics: object, system: object}`.

Target: `{id,name,engine,url,model?,apiKeyEnv?,metricsUrl?,status?,error?}`. POST `/api/v1/targets` accepts target fields; DELETE `/api/v1/targets/{id}`. GET corresponding collections: targets/models/gpus/requests/benchmarks/experiments/events/metrics. POST `/api/v1/discover` registers only loopback discovery results. POST `/api/v1/benchmarks` starts async benchmark, returns 202 `{id}`. GET `/api/v1/benchmarks/{id}` returns job `{id,status,error?,run?}`. DELETE same path cancels. GET `/api/v1/compare?baseline=ID&candidate=ID` returns comparison. GET `/api/v1/stream` SSE `snapshot` events with same snapshot shape. GET `/healthz`, `/metrics`.

Benchmark config: `{targetId,model,prompt,concurrency: number[],durationSeconds,maxTokens,name?}`. Never persist prompt. Run shared engine/bench types will be integrated by runtime owner. Frontend consume tolerant typed shapes below and normalize at runtime if needed.

Model: `{id,targetId,engine}`. GPU: `{index,uuid,name,utilization?,memoryUsedMB?,memoryTotalMB?,memoryUtilization?,temperatureC?,powerW?,powerLimitW?,smClockMHz?,memoryClockMHz?,pcieRxMBs?,pcieTxMBs?,nvlinkRxMBs?,nvlinkTxMBs?,eccErrors?,xidErrors?,throttled?,source,topology?}`.

RequestResult: `{id,targetId,model,startedAt,durationMs,ttftMs?,tpotMs?,outputTokens?,inputTokens?,status,error?,chunkGapsMs?,concurrency?}`. Finding: `{kind,severity,confidence,summary,evidence: string[],recommendation}`.

Run: `{id,name,targetId,model,startedAt,status,config,points: BenchPoint[],findings: Finding[],knee?}`. BenchPoint: `{concurrency,requests,errors,durationSeconds,rps,outputTokensPerSecond,ttft:{p50,p95,p99,mean,max}?,tpot:{p50,p95,p99,mean,max}?,e2e:{p50,p95,p99,mean,max},gpuSamples?}`. SnapshotPoint: `{time,tokensPerSecond?,rps?,ttftMs?,gpuUtilization?,kvCachePercent?}`. Demo fixtures may populate these shapes but must remain visibly labeled and never appear in CLI/live mode.
