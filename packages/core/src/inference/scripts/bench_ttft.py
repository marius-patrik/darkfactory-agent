#!/usr/bin/env python3
"""VS1 long-context TTFT benchmark — issues #1262 + #1258 epic acceptance.

Measures streaming TTFT and decode tok/s for conv-7b-1m and conv-14b-1m at
context sizes 4k / 32k / 128k tokens (512k optional if 128k finished under 5 min).
Each measurement is time-boxed at 25 minutes; on timeout the result is recorded
as 'exceeded 25m'.  llama.cpp's reported prompt-eval tok/s is also captured from
the final streaming chunk's ``timings`` object.

Run:
    python scripts/bench_ttft.py [--gateway-url URL]
    GATEWAY_URL=http://host:port python scripts/bench_ttft.py

Default GATEWAY_URL = http://127.0.0.1:8800
"""
from __future__ import annotations

import argparse
import json
import sys
import time
import socket
import threading
import os
from dataclasses import dataclass, field
from typing import Any

# ---------------------------------------------------------------------------
# Config
# ---------------------------------------------------------------------------
DEFAULT_GATEWAY = os.environ.get("GATEWAY_URL", "http://127.0.0.1:8800")

BENCH_MODELS = ["conv-7b-1m", "conv-14b-1m"]
# Target context sizes in tokens (approximate; haystack generator will match)
CONTEXT_SIZES = [4_096, 32_768, 131_072]   # 4k, 32k, 128k
OPTIONAL_512K = 524_288  # added only if 128k finishes under 5 min

TIMEOUT_SECONDS = 25 * 60  # 25 minutes per measurement
NEEDLE = "THE-NEEDLE-IS-42"
NEEDLE_Q = (
    "Based on the text above, what special value was hidden in the needle sentence? "
    "Reply with only the number."
)
# Haystack paragraph (~100 tokens each, ~400 chars)
PARA = (
    "The following passage is a synthetic context-filler paragraph used for "
    "benchmarking purposes only.  It contains no meaningful information and is "
    "designed to consume tokens in a predictable and reproducible manner. "
    "The text is repeated many times to reach the desired context length. "
)
TOKENS_PER_PARA = 67  # conservative estimate (actual measured ~67 tokens)
# Max tokens to generate (we just need the first token for TTFT)
MAX_NEW_TOKENS = 32
DECODE_SAMPLE_TOKENS = 16  # minimum tokens to measure decode rate reliably

# ---------------------------------------------------------------------------
# Data model
# ---------------------------------------------------------------------------


@dataclass
class BenchResult:
    model_id: str
    context_tokens_target: int
    context_tokens_actual: int = 0
    ttft_ms: float | None = None          # wall-clock ms to first content token
    decode_tps: float | None = None       # predicted_per_second from llama.cpp
    llamacpp_prompt_tps: float | None = None  # prompt_per_second from llama.cpp
    gateway_duration_ms: float | None = None
    error: str | None = None
    timed_out: bool = False
    needle_found: bool = False


# ---------------------------------------------------------------------------
# Haystack builder
# ---------------------------------------------------------------------------


def build_haystack(target_tokens: int) -> tuple[str, int]:
    """Build a synthetic haystack of approximately *target_tokens* tokens.

    Returns (system_prompt_text, estimated_tokens).
    The needle is planted 10 % from the end so the model must process most
    of the context.  Estimation uses 4-chars-per-token heuristic.
    """
    target_chars = target_tokens * 4  # 4 chars/token heuristic
    # Reserve space for needle + question
    needle_sentence = f"\n\nHIDDEN VALUE: {NEEDLE} (remember this).\n\n"
    question_chars = len(NEEDLE_Q) * 4 + 100  # rough margin
    fill_chars = max(0, target_chars - len(needle_sentence) - question_chars)
    paras_needed = max(1, fill_chars // len(PARA))

    # Build fill text, plant needle 90% through
    pre_needle_paras = int(paras_needed * 0.90)
    post_needle_paras = paras_needed - pre_needle_paras

    haystack = (
        PARA * pre_needle_paras
        + needle_sentence
        + PARA * post_needle_paras
    )
    estimated = len(haystack) // 4
    return haystack, estimated


# ---------------------------------------------------------------------------
# Streaming HTTP (stdlib only)
# ---------------------------------------------------------------------------


def _parse_gateway_url(url: str) -> tuple[str, int, str]:
    """Parse 'http://host:port' into (host, port, base_path)."""
    url = url.rstrip("/")
    if url.startswith("http://"):
        rest = url[7:]
    elif url.startswith("https://"):
        rest = url[8:]
    else:
        rest = url
    if "/" in rest:
        hostport, base_path = rest.split("/", 1)
        base_path = "/" + base_path
    else:
        hostport = rest
        base_path = ""
    if ":" in hostport:
        host, port_s = hostport.rsplit(":", 1)
        port = int(port_s)
    else:
        host = hostport
        port = 80
    return host, port, base_path


def _stream_completion_raw(
    gateway: str,
    model_id: str,
    messages: list[dict],
    max_tokens: int,
    timeout: float,
) -> BenchResult:
    """Send a streaming chat completion and measure TTFT.

    Returns a BenchResult with timing data extracted from the stream.
    Uses raw socket to have precise wall-clock control over the first byte.
    """
    host, port, base_path = _parse_gateway_url(gateway)
    endpoint = base_path + "/v1/chat/completions"
    payload = json.dumps({
        "model": model_id,
        "messages": messages,
        "stream": True,
        "max_tokens": max_tokens,
        "temperature": 0.0,
    }).encode()

    result = BenchResult(
        model_id=model_id,
        context_tokens_target=0,  # set by caller
    )

    headers = (
        f"POST {endpoint} HTTP/1.1\r\n"
        f"Host: {host}:{port}\r\n"
        f"Content-Type: application/json\r\n"
        f"Content-Length: {len(payload)}\r\n"
        f"Connection: close\r\n"
        f"\r\n"
    ).encode()

    try:
        sock = socket.create_connection((host, port), timeout=min(30, timeout))
        sock.settimeout(timeout)

        t_send = time.perf_counter()
        sock.sendall(headers + payload)

        # Read HTTP response headers
        buf = b""
        while b"\r\n\r\n" not in buf:
            chunk = sock.recv(4096)
            if not chunk:
                result.error = "Connection closed before headers"
                return result
            buf += chunk

        header_end = buf.index(b"\r\n\r\n") + 4
        http_header = buf[:header_end].decode(errors="replace")
        remaining = buf[header_end:]

        # Check HTTP status
        first_line = http_header.split("\r\n")[0]
        if not first_line.startswith("HTTP/") or " 200 " not in first_line:
            result.error = f"Non-200 HTTP status: {first_line.strip()}"
            return result

        # Stream SSE lines
        line_buf = remaining.decode(errors="replace")
        ttft_measured = False
        t_first_token: float | None = None
        timings: dict[str, Any] | None = None
        chunk_count = 0
        content_chunks: list[str] = []

        while True:
            while "\n" in line_buf:
                nl_pos = line_buf.index("\n")
                line = line_buf[:nl_pos].rstrip("\r")
                line_buf = line_buf[nl_pos + 1:]

                if not line:
                    continue
                if line.startswith("data: "):
                    data_str = line[6:].strip()
                    if data_str == "[DONE]":
                        # Done — finalize
                        goto_done = True
                        break
                    try:
                        chunk_data = json.loads(data_str)
                    except json.JSONDecodeError:
                        continue

                    # Check for timings (last content chunk from llama.cpp)
                    if "timings" in chunk_data:
                        timings = chunk_data["timings"]

                    choices = chunk_data.get("choices", [])
                    if choices:
                        delta = choices[0].get("delta", {})
                        content = delta.get("content")
                        if content:
                            if not ttft_measured:
                                t_first_token = time.perf_counter()
                                ttft_measured = True
                            content_chunks.append(content)
                            chunk_count += 1
            else:
                # Need more data
                try:
                    raw = sock.recv(65536)
                    if not raw:
                        break
                    line_buf += raw.decode(errors="replace")
                    continue
                except socket.timeout:
                    result.timed_out = True
                    result.error = "exceeded 25m"
                    break
            break  # goto_done

        sock.close()

        if t_first_token is not None:
            result.ttft_ms = round((t_first_token - t_send) * 1000, 1)

        if timings:
            result.llamacpp_prompt_tps = round(timings.get("prompt_per_second", 0), 2)
            result.decode_tps = round(timings.get("predicted_per_second", 0), 2)
            result.gateway_duration_ms = None  # not in stream timings chunk

        full_content = "".join(content_chunks).strip()
        result.needle_found = "42" in full_content

    except socket.timeout:
        result.timed_out = True
        result.error = "exceeded 25m"
    except Exception as exc:
        result.error = f"{type(exc).__name__}: {exc}"

    return result


# ---------------------------------------------------------------------------
# Benchmark runner
# ---------------------------------------------------------------------------


def run_one(
    gateway: str,
    model_id: str,
    context_tokens: int,
    timeout: float = TIMEOUT_SECONDS,
) -> BenchResult:
    haystack, estimated_tokens = build_haystack(context_tokens)
    messages = [
        {"role": "user", "content": haystack + "\n\n" + NEEDLE_Q},
    ]

    print(
        f"    Running {model_id} @ ~{context_tokens // 1000}k tokens "
        f"(estimated {estimated_tokens:,} tokens) ...",
        flush=True,
    )

    # Wrap in a thread for clean timeout enforcement
    result = BenchResult(
        model_id=model_id,
        context_tokens_target=context_tokens,
        context_tokens_actual=estimated_tokens,
    )

    container: list[BenchResult] = []

    def _run():
        r = _stream_completion_raw(gateway, model_id, messages, MAX_NEW_TOKENS, timeout)
        r.context_tokens_target = context_tokens
        r.context_tokens_actual = estimated_tokens
        container.append(r)

    t = threading.Thread(target=_run, daemon=True)
    t_start = time.perf_counter()
    t.start()
    t.join(timeout=timeout + 5)  # +5s grace for socket teardown
    elapsed = time.perf_counter() - t_start

    if container:
        result = container[0]
    else:
        result.timed_out = True
        result.error = "exceeded 25m"

    if result.ttft_ms is not None:
        print(
            f"      TTFT={result.ttft_ms:.0f}ms  "
            f"decode={result.decode_tps} tok/s  "
            f"prompt-eval={result.llamacpp_prompt_tps} tok/s  "
            f"needle={'FOUND' if result.needle_found else 'MISSING'}",
            flush=True,
        )
    elif result.timed_out:
        print(f"      TIMEOUT after {elapsed/60:.1f} min", flush=True)
    else:
        print(f"      ERROR: {result.error}", flush=True)

    return result


def run_bench(gateway: str) -> list[BenchResult]:
    print(f"\nRommie VS1 TTFT Benchmark — gateway: {gateway}")
    print("=" * 60)
    all_results: list[BenchResult] = []

    for model_id in BENCH_MODELS:
        print(f"\nModel: {model_id}")
        sizes_to_run = list(CONTEXT_SIZES)
        last_128k_under_5min = False

        for ctx_size in sizes_to_run:
            t0 = time.perf_counter()
            r = run_one(gateway, model_id, ctx_size)
            elapsed = time.perf_counter() - t0
            all_results.append(r)

            if ctx_size == 131_072 and not r.timed_out and r.error is None:
                if elapsed < 5 * 60:
                    last_128k_under_5min = True

        # Optional 512k
        if last_128k_under_5min:
            print(f"  128k finished in <5min — attempting optional 512k measurement")
            r = run_one(gateway, model_id, OPTIONAL_512K)
            all_results.append(r)
        else:
            print(f"  Skipping optional 512k (128k did not finish under 5 min or timed out)")

    return all_results


# ---------------------------------------------------------------------------
# Markdown table printer
# ---------------------------------------------------------------------------


def format_ms(v: float | None) -> str:
    if v is None:
        return "—"
    if v >= 60_000:
        return f"{v/60000:.1f}m"
    if v >= 1000:
        return f"{v/1000:.1f}s"
    return f"{v:.0f}ms"


def format_tps(v: float | None) -> str:
    if v is None:
        return "—"
    return f"{v:.1f}"


def print_markdown_table(results: list[BenchResult]) -> str:
    lines = []
    lines.append("")
    lines.append("## TTFT Results")
    lines.append("")
    lines.append(
        "| Model | Context | Est. tokens | TTFT | Decode tok/s | Prompt-eval tok/s | Note |"
    )
    lines.append(
        "|-------|---------|-------------|------|-------------|-------------------|------|"
    )
    for r in results:
        ctx_label = f"{r.context_tokens_target // 1024}k"
        note = ""
        if r.timed_out:
            note = "exceeded 25m"
        elif r.error:
            note = f"error: {r.error[:50]}"
        elif not r.needle_found:
            note = "needle not found in reply"
        lines.append(
            f"| {r.model_id} | {ctx_label} | {r.context_tokens_actual:,} "
            f"| {format_ms(r.ttft_ms)} | {format_tps(r.decode_tps)} "
            f"| {format_tps(r.llamacpp_prompt_tps)} | {note} |"
        )
    lines.append("")
    table = "\n".join(lines)
    print(table)
    return table


# ---------------------------------------------------------------------------
# Entry point
# ---------------------------------------------------------------------------


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="VS1 TTFT benchmark")
    parser.add_argument(
        "--gateway-url",
        default=DEFAULT_GATEWAY,
        help="Gateway base URL (default: GATEWAY_URL env or http://127.0.0.1:8800)",
    )
    # Allow positional URL for backwards compatibility
    parser.add_argument("url", nargs="?", default=None, help="Gateway URL (positional, deprecated)")
    args = parser.parse_args()
    gateway_url = (args.url or args.gateway_url).rstrip("/")

    all_results = run_bench(gateway_url)
    table = print_markdown_table(all_results)
    # Write results to a temp JSON for the doc-writer step
    out_path = os.path.join(
        os.path.dirname(os.path.abspath(__file__)),
        ".bench_results.json",
    )
    with open(out_path, "w") as f:
        json.dump(
            [
                {
                    "model_id": r.model_id,
                    "context_tokens_target": r.context_tokens_target,
                    "context_tokens_actual": r.context_tokens_actual,
                    "ttft_ms": r.ttft_ms,
                    "decode_tps": r.decode_tps,
                    "llamacpp_prompt_tps": r.llamacpp_prompt_tps,
                    "gateway_duration_ms": r.gateway_duration_ms,
                    "error": r.error,
                    "timed_out": r.timed_out,
                    "needle_found": r.needle_found,
                }
                for r in all_results
            ],
            f,
            indent=2,
        )
    print(f"\nRaw results saved to: {out_path}")
