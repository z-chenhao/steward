#!/usr/bin/env python3
"""Mandatory default-composition Computer business gate; no native fallback."""
import argparse
import json
from pathlib import Path

from verify_computer_use import run


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--evidence', type=Path, required=True)
    args = parser.parse_args()
    report = run(args.binary.resolve(), managed_plugins=True)
    args.evidence.parent.mkdir(parents=True, exist_ok=True)
    args.evidence.write_text(json.dumps(report, ensure_ascii=False, indent=2)+'\n')
    print(json.dumps(dict(evidence=str(args.evidence), checks=len(report['checks']),
                         default_packages=len(report['default_plugin_packages']), cleanup=report['cleanup'])))


if __name__ == '__main__':
    main()
