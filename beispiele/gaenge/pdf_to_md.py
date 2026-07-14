#!/usr/bin/env python3
"""pdf_to_md.py <input.pdf> --out <out.md>

Deterministische PDF-Extraktion für den Referenz-Auftrag pdf-einlagern
(PLAN.md §6): shellt nach pdftotext (poppler) raus, kein LLM. Exit != 0
bei kaputtem PDF oder leerem Extrakt — der Runner bricht dann ab und
der Input wandert nach quarantaene/ (§7), nie nach archiv/.
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
        sys.exit("pdf_to_md: kein Text extrahiert — Scan ohne OCR?")

    heute = datetime.date.today().isoformat()
    name = pathlib.Path(args.pdf).name
    pathlib.Path(args.out).write_text(
        f"# Extrakt aus {name}\n\nExtrahiert am: {heute}\n\n{text}\n",
        encoding="utf-8",
    )


if __name__ == "__main__":
    main()
