"""Warm-path TTFT probe: persistent-slot prompt-cache vs cold full-context eval.

Three sequential turns against one model through the gateway: turn 1 pays full
prompt-eval (cold); turns 2-3 share the prefix and measure the warm TTFT the
standing brain actually experiences. See docs/benchmarks/vs1-ttft-2026-06-12.md.
"""

import argparse
import json
import os
import time
import urllib.request

PARA = (
    "The quarterly logistics report noted that container throughput rose steadily. "
    "Dock scheduling, crane allocation, and customs pre-clearance all contributed. "
)
NEEDLE = "The secret code word hidden in this report is MARMALADE. "


def ask(url: str, model: str, haystack: str, question: str) -> tuple[float, float, str]:
    body = json.dumps(
        {
            "model": model,
            "stream": True,
            "messages": [{"role": "user", "content": haystack + "\n\nQuestion: " + question}],
            "max_tokens": 40,
        }
    ).encode()
    req = urllib.request.Request(url, body, {"Content-Type": "application/json"})
    t0 = time.time()
    first = None
    text = ""
    with urllib.request.urlopen(req, timeout=3600) as resp:
        for line in resp:
            line = line.decode().strip()
            if not line.startswith("data: ") or line == "data: [DONE]":
                continue
            try:
                chunk = json.loads(line[6:])["choices"][0]["delta"].get("content") or ""
            except (KeyError, IndexError, json.JSONDecodeError):
                continue
            if chunk and first is None:
                first = time.time() - t0
            text += chunk
    return first or -1.0, time.time() - t0, text.strip()[:60]


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--gateway-url", default=os.environ.get("GATEWAY_URL", "http://127.0.0.1:8800"))
    parser.add_argument("--model", default="conv-7b-1m")
    parser.add_argument("--paragraphs", type=int, default=220, help="~8k tokens at the default")
    args = parser.parse_args()

    url = args.gateway_url.rstrip("/") + "/v1/chat/completions"
    hay = PARA * args.paragraphs
    hay = hay[: len(hay) // 2] + NEEDLE + hay[len(hay) // 2 :]

    turns = [
        ("turn1-COLD", "What is the secret code word? Reply with only the word."),
        ("turn2-WARM-same-prefix", "What rose steadily according to the report? Answer in 3 words."),
        ("turn3-WARM-again", "What is the secret code word again? Only the word."),
    ]
    for label, question in turns:
        ttft, total, answer = ask(url, args.model, hay, question)
        print(f"{label}: TTFT={ttft:.1f}s total={total:.1f}s answer={answer!r}", flush=True)


if __name__ == "__main__":
    main()
