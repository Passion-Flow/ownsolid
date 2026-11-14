# Model contract fixture

`qwen3-config.json` is the exact small upstream Qwen/Qwen3-0.6B config.json from revision c1899de289a04d12100db370d81485cdf75e47ca, covered by its Apache-2.0 license. The compiled catalog records its expected SHA-256 and size. It verifies bounded file transfer through real TLS without copying weights into Git. The full actual model import/cache profiles use separately frozen external assets; this fixture is not load or GPU qualification.
