#!/usr/bin/env python3
"""Six real guided settings flows with production managed form dispatch."""
import argparse
import hashlib
import json
from pathlib import Path
from fixture_pool import run_cases
from verify_channels import capability_settings

def run(binary,evidence):
 evidence.parent.mkdir(parents=True,exist_ok=True)
 report=dict(checks=[],scenarios={},binary_sha256=hashlib.sha256(binary.read_bytes()).hexdigest())
 capability_settings(binary,evidence,report,managed=True)
 report['scope']='F030 managed forms, catalogue, diagnostics and sealed original view/copy; Host authority and explicit cross-feature preset audit retained'
 evidence.write_text(json.dumps(report,indent=2)+'\n')
 return dict(checks=report['checks'],binary_sha256=report['binary_sha256'])
if __name__=='__main__':
 parser=argparse.ArgumentParser();parser.add_argument('--binary',type=Path,required=True);parser.add_argument('--evidence',type=Path,required=True);args=parser.parse_args()
 print(json.dumps(run_cases([0],lambda _:run(args.binary.resolve(),args.evidence.resolve()),1)[0]))
