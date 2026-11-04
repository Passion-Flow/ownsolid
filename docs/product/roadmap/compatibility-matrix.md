# Compatibility matrix

Every vendor-named target lives here as a later item, qualified on demand. Contract items
in the owning modules come first; an entry here means "adapter built and tested for this
product". Each entry gets its own box for real-environment verification.

- Depends on: the owning module's contract item.

## P2 Fleet operations

- [ ] CMP-201 Domestic business databases: Dameng, KingbaseES, openGauss and its family (P2) - Catalog: F244, F245, F247, F518
- [ ] CMP-202 MySQL as the business database (P2) - Catalog: F238
  - CMP-201 and CMP-202 must add the `site_id` and `cell_id` columns their chains lack (ADR 0006).
- [ ] CMP-203 Domestic CPUs and operating systems: Kunpeng, Hygon, Kylin, UOS, openEuler (P2) - Catalog: F512 to F517
- [ ] CMP-204 Harbor registry (P2) - Catalog: F212
- [ ] CMP-205 MinIO, Ceph RGW and SeaweedFS object storage (P2) - Catalog: F211, F320, F321
- [ ] CMP-206 Valkey and Redis caches (P2) - Catalog: F209
- [ ] CMP-207 NATS JetStream, Kafka and RabbitMQ (P2) - Catalog: F213, F343, F346
- [ ] CMP-208 Private clouds: OpenShift, Rancher, OpenStack, Huawei Cloud Stack, Apsara Stack, TCE, veStack (P2) - Catalog: F383, F386 to F390
- [ ] CMP-209 Identity products: Entra ID, Okta, Keycloak, Authing, Feishu, DingTalk, WeCom (P2) - Catalog: F458 to F463
- [ ] CMP-210 Public clouds for resource import: AWS, Huawei Cloud, Tencent Cloud, Alibaba Cloud, Volcengine (P2) - Catalog: F359, F362 to F365

## P4 Breadth

- [ ] CMP-401 Remaining relational databases (P4) - Catalog: F197, F239 to F263, F412, F413
- [ ] CMP-402 Specialty databases as data sources (P4) - Catalog: F265 to F279, F414 to F421
- [ ] CMP-403 Vector stores, only where an app needs them (P4) - Catalog: F198, F203, F206 to F208, F214, F281 to F299
- [ ] CMP-404 Remaining caches (P4) - Catalog: F201, F300 to F310, F422
- [ ] CMP-405 Remaining object and file storage (P4) - Catalog: F311 to F328, F423
- [ ] CMP-406 Remaining registries (P4) - Catalog: F331 to F341
- [ ] CMP-407 Remaining messaging and configuration services (P4) - Catalog: F202, F344, F345, F347 to F349
- [ ] CMP-408 Remaining public and private clouds (P4) - Catalog: F360, F361, F366 to F382, F384, F385, F391 to F394
- [ ] CMP-409 Inference engines beyond vLLM, SGLang and MindIE (P4) - Catalog: F156, F159 to F165, F168 to F170, F182
- [ ] CMP-410 Training frameworks and gateways (P4) - Catalog: F088, F089, F126 to F139
- [ ] CMP-411 Agent frameworks (P4) - Catalog: F046 to F050
- [ ] CMP-412 Other hardware: Apple silicon, TPU, AWS Neuron (P4) - Catalog: F189, F195, F196
