# CompatProbe

Diagnose why an OpenAI-compatible API works in a simple request but breaks in real AI clients.

```text
CompatProbe

Probes
----------------------------------
authentication_models            PASS  42 ms
basic_chat_completion            PASS  1.21 s
stream_completion                FAIL  43.12 s

Diagnosis
----------------------------------
HIGH  STREAM_TRANSPORT_INTERRUPTION
Streaming connection terminated before protocol completion.
```

CompatProbe is a local, standard-library-only Go CLI. It records protocol and network facts, separates observations from diagnosis, and produces reports suitable for a GitHub issue. It is not a chat client, benchmark, gateway, or model authenticity test.

## Installation

Build from a checkout with Go 1.23 or newer:

```bash
go install ./cmd/compatprobe
```

Or download a platform binary from [GitHub Releases](https://github.com/ivysterling59-cyber/compatprobe/releases). Windows, Linux, and macOS builds are available for amd64 and arm64; verify downloads against the release's `SHA256SUMS` file.

## Quick Start

Prefer supplying credentials through the environment:

```bash
export COMPATPROBE_API_KEY="sk-..."
compatprobe check \
  --base-url https://example.com/v1 \
  --model example-model
```

On PowerShell:

```powershell
$env:COMPATPROBE_API_KEY = "sk-..."
compatprobe check --base-url https://example.com/v1 --model example-model
```

Quick compatibility test (uses the conservative 256-token completion budget):

```bash
compatprobe check \
  --base-url https://example.com/v1 \
  --model example-model
```

Extended stream reproduction:

```bash
compatprobe check \
  --base-url https://example.com/v1 \
  --model example-model \
  --stream-tokens 2048 \
  --timeout 120s
```

`--stream-tokens` accepts values from 16 through 32768 and defaults to 256. It maps to the Chat Completions `max_completion_tokens` completion-token budget, which can include visible output tokens and reasoning tokens. It does not guarantee a number of visible output tokens or a particular stream duration. Extended runs can consume more API tokens, and generation speed varies by model and provider.

Machine-readable JSON and a shareable Markdown report:

```bash
compatprobe check --base-url https://example.com/v1 --model example-model --json
compatprobe check --base-url https://example.com/v1 --model example-model --output compatprobe-report.md
```

Use `--timeout 90s` to set the overall check deadline, `--verbose`, or comma-separated custom headers with `--header 'Name:Value,X-Other:Value'` when needed. Press Ctrl+C to cancel active requests. Run `compatprobe help` for the command summary.

## What It Tests

- Authentication and optional model listing with `GET /v1/models`
- Non-streaming Chat Completions JSON
- Streaming Chat Completions over Server-Sent Events
- SSE framing, including LF/CRLF, comments, metadata fields, and multi-line data
- Completion semantics: `finish_reason`, `[DONE]`, and clean EOF
- Configured `max_completion_tokens` budget alongside actual observed duration and event counts
- DNS, TCP, TLS, connection reuse, request write, response headers, TTFB, and first SSE event timeline facts

A missing `/models` endpoint is a warning rather than proof that the API is incompatible. A missing `[DONE]` marker is recorded as a fact and interpreted with other evidence.

## Example Diagnosis

`STREAM_TRANSPORT_INTERRUPTION` is emitted only when non-streaming succeeds, valid SSE data was received, normal completion was not observed, and the stream ended with an interruption-like error. Suggestions name possible layers such as a reverse proxy or network intermediary; CompatProbe does not assign a provider-specific root cause without direct evidence.

## Security / API Keys

CompatProbe does not upload your API keys or diagnostic reports. Requests go only to the configured endpoint. API keys are not stored in reports or configuration files. Output is filtered for authorization, proxy authorization, API key, cookie, and bearer-token patterns; conservative over-redaction is intentional.

Environment variables reduce accidental exposure in command histories. `--api-key` remains available for scripting but is not recommended.

## Supported Protocols

Phase 1 implements OpenAI-compatible `/v1/models` and the [`/v1/chat/completions` Create Chat Completion endpoint](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create). Base URLs both with and without a trailing `/v1` are supported. Responses API and tool calling are not implemented.

## Exit Codes

- `0`: all critical checks passed (warnings may be present)
- `1`: a compatibility failure was detected
- `2`: configuration, usage, or report-write error
- `3`: the network target could not be meaningfully tested

## Error Classification

Reports use stable error kinds for `authentication`, `rate_limit`, `server`, `http_status`, `dns`, `tls`, `connection_refused`, `network_unreachable`, `timeout`, `cancellation`, and `unexpected_eof`. These describe the observed layer; they do not claim a provider-specific root cause.

The timeout is an overall budget for the complete check. If the target is unreachable or the check is cancelled, remaining probes are marked `SKIP` instead of retrying the same unavailable target.

## Roadmap

After Phase 1 is stable, possible work includes tool-call stream reconstruction, Responses API, HTTP version comparison, long-stream stability, cancellation behavior, and client compatibility profiles. None of these are included today.

## Contributing

Run the complete local checks before opening a change:

```bash
go test ./...
go test -race ./...
go vet ./...
```

Keep probes factual, put causal inference in diagnosis rules, and add a deterministic test-server scenario for each diagnosis.
