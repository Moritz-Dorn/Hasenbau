#!/usr/bin/env python3
"""pdf_to_md.py <input.pdf> --out <out.md>

Deterministic PDF extraction for the reference Auftrag pdf-einlagern
(PLAN.md §6): shells out to pdftotext (poppler), no LLM. Exit != 0 on a
broken PDF or an empty extract — the runner then aborts and the input
goes to quarantaene/ (§7), never to archiv/.
"""

import argparse
import datetime
import pathlib
import subprocess
import sys


def main() -> None:
    p = argparse.ArgumentParser()
    p.add_argument("pdf")
    p.add_argument("--out", required=True)
    args = p.parse_args()

    res = subprocess.run(
        ["pdftotext", "-layout", "-enc", "UTF-8", args.pdf, "-"],
        capture_output=True,
        text=True,
    )
    if res.returncode != 0:
        sys.stderr.write(res.stderr)
        sys.exit(res.returncode)
    text = res.stdout.strip()
    if not text:
        sys.exit("pdf_to_md: no text extracted — a scan without OCR?")

    heute = datetime.date.today().isoformat()
    name = pathlib.Path(args.pdf).name
    pathlib.Path(args.out).write_text(
        f"# Extract from {name}\n\nExtracted on: {heute}\n\n{text}\n",
        encoding="utf-8",
    )


if __name__ == "__main__":
    main()
