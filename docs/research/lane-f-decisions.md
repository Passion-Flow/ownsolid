# Lane F decisions

## DAT-101 intake and product boundary

**Decision.** Build an additive bounded Go intake API for upload bodies and
authorized S3 objects. Use the existing S3 transport and run Arrow in the
Python data runtime. Commit private JSONL only after the source and its
optional receipt are verified. Return statistics to the product caller.
Keep the existing training draft, API, schema, and release image unchanged.

**Why.** The draft API and worker contract enforce a 64 MiB and 10,000-row
limit and materialize records in SQL. That path cannot meet the DAT-101 size
or generic-field requirements. The lane owns the datasets and data runtime
directories. The S3 client already owns endpoint and credential-file
configuration and constrains reads to an exact object.

**Alternatives.** Raising the draft limits would scale memory with row count
and change shared contracts. Reading a caller-supplied object URL would bypass
storage and workspace authority. Adding a Parquet module to the Go dependency
graph would change shared dependency and release inputs. Guessing legacy CSV
encoding can silently alter data that is valid in two encodings.

**Impact.** The original draft behavior remains compatible. A new product
caller must bind the intake result to authenticated workspace authority and
persist its report. A wrong format or GBK selection is recoverable by
resubmitting the source with the correct declaration.

**Boundary.** Keep DAT-101 unticked until the Console/Portal upload, durable
dataset registration and report readback, and the pinned Arrow offline image
are verified together. The existing object-ingestion product caller is outside
Lane F and still targets the legacy JSONL draft flow. Product completion
requires the relevant UI, API/composition, storage, and release owners to
integrate the new boundary. This lane's verification covers the intake API.
