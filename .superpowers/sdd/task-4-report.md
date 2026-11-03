# Task 4 Report: Add the WhatsApp `/status` Command

## Summary

Implemented a read-only WhatsApp `/status` command that is registered only when the Meet monitor service exists, formats status output exactly as specified, and uses the same monitor service pointer already passed to the HTTP API.

## Files Changed

- `cmd/manager.go`
- `cmd/handlers.go`
- `cmd/handlers_test.go`
- `main.go`
- `main_test.go`

## RED Evidence

### RED 1: command-layer tests

Command:

```powershell
go test ./cmd -run "Status|Help" -count=1
```

Observed failure:

```text
# whatsappBot/cmd [whatsappBot/cmd.test]
cmd\handlers_test.go:435:3: undefined: WithStatusProvider
cmd\handlers_test.go:435:3: too many arguments in call to NewCommandManager
	have (*fakeSender, nil, nil, unknown type, unknown type)
	want (utils.MessageSender, []string, context.CancelFunc)
cmd\handlers_test.go:436:3: undefined: WithClock
cmd\handlers_test.go:471:49: undefined: WithStatusProvider
cmd\handlers_test.go:471:49: too many arguments in call to NewCommandManager
	have (*fakeSender, nil, nil, unknown type)
	want (utils.MessageSender, []string, context.CancelFunc)
cmd\handlers_test.go:512:3: undefined: WithStatusProvider
cmd\handlers_test.go:512:3: too many arguments in call to NewCommandManager
	have (*fakeSender, nil, nil, unknown type, unknown type)
	want (utils.MessageSender, []string, context.CancelFunc)
cmd\handlers_test.go:513:3: undefined: WithClock
cmd\handlers_test.go:552:5: undefined: WithStatusProvider
cmd\handlers_test.go:564:5: undefined: WithClock
cmd\handlers_test.go:564:5: too many errors
FAIL	whatsappBot/cmd [build failed]
FAIL
```

### RED 2: runtime wiring test

Command:

```powershell
go test . -run "SameMonitorServiceForCommands" -count=1
```

Observed failure:

```text
Existing session found. Connecting...
Bot is running. Press Ctrl+C to exit.
--- FAIL: TestRunEnabledAPIUsesSameMonitorServiceForCommands (2.77s)
    main_test.go:416: sent 0 messages, want 1
FAIL
FAIL	whatsappBot	4.973s
FAIL
```

## GREEN Evidence

Formatting command:

```powershell
gofmt -w cmd/manager.go cmd/handlers.go cmd/handlers_test.go main.go main_test.go
```

Command:

```powershell
go test ./cmd -count=1
```

Output:

```text
ok  	whatsappBot/cmd	3.823s
```

Command:

```powershell
go test . -count=1
```

Output:

```text
ok  	whatsappBot	2.450s
```

Command:

```powershell
go test ./... -count=1
```

Output:

```text
ok  	whatsappBot	1.413s
ok  	whatsappBot/cmd	3.242s
ok  	whatsappBot/config	0.169s
ok  	whatsappBot/db	1.315s
ok  	whatsappBot/meetmonitor	6.697s
ok  	whatsappBot/messageapi	9.000s
ok  	whatsappBot/outbound	2.489s
ok  	whatsappBot/scheduler	1.858s
ok  	whatsappBot/tone	0.894s
ok  	whatsappBot/utils	0.761s
```

## Tests Added or Updated

- Exact `/status` online response
- Exact `/status` never-seen response
- Offline minutes formatting and forwarding OFF
- Seconds, hours, and future-heartbeat clamping
- Disconnected WhatsApp state
- Nil heartbeat fallback
- Provider called exactly once
- Provider error with no reply
- Reply-send error propagation
- Case-insensitive `/STATUS`
- Regular user access
- `/help` includes `/status` when provider exists
- Manager without provider does not register or advertise `/status`
- API-enabled runtime uses the same monitor service pointer for commands and HTTP API
- API-disabled startup remains valid (existing coverage kept green)

## Self-Review

- Confirmed `/status` is only registered when a status provider is present, so reminder-only mode is preserved when the API is disabled.
- Confirmed `main.go` reuses the existing `monitorService` instance and does not construct a second Meet monitor service.
- Confirmed command handling stays read-only through the `StatusProvider` interface and does not touch SQLite directly.
- Confirmed status formatting matches the specified exact strings, including `never seen`, `unknown`, and floored age formatting with future clamping.
- Confirmed existing three-argument `NewCommandManager(...)` call sites still compile because the constructor remains backward-compatible via variadic options.

## Concerns

- No functional concerns identified.
- The pointer-sharing runtime test uses `reflect`/`unsafe` to mutate the captured service's private `meeting` field so the test can prove exact pointer reuse without widening production APIs. This is intentional and test-only.
