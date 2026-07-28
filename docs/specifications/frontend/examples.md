# Frontend examples

## Wire sequence — attach and chat

```
Frontend                         Kernel (callback channel)
   |                                    |
   |-- CreateSession(profile=default) ->|
   |<- SessionInfo (session_id=S) ------|
   |-- GetSessionState(S) ------------->|
   |<- SessionState --------------------|
   |-- ListMetadata(S) ---------------->|
   |<- blocks[] ------------------------|
   |-- Subscribe([kernel.event.*,       |
   |              kernel.state,           |
   |              kernel.metadata]) --->|
   |-- StreamDeltas(S) ---------------->|  (open for life of session)
   |-- SubmitInput(S, [Text("hi")]) --->|
   |<- turn_id=T -----------------------|
   |<- BusEvent kernel.event.message ---|
   |<- TokenDelta{target, "Hel"} -------|
   |<- TokenDelta{target, "lo"} --------|
   |<- BusEvent kernel.event.message ---|  (finished render path)
```

## Metadata block

A widget (or tool) publishes:

```
PublishMetadata(session_id=S, block={
  id: "git.branch",
  priority: 10,
  tone: TONE_INFO,
  body: KeyValue{key: "branch", value: "main"},
})
```

Kernel stamps `producer` and `liveness=LIVE`, stores, and publishes on topic `kernel.metadata`. On plugin exit, each LIVE block flips to `DISCONNECTED` and is republished; frontends decide gray vs drop.
