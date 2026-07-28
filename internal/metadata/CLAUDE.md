# metadata

- Never delete a block; only flip liveness.
- Producer identity is always server-derived — never trust a client-set `producer` field on the incoming block.
- Topic is the fixed string `kernel.metadata`; `session_id` is on the payload, not in the topic name.
