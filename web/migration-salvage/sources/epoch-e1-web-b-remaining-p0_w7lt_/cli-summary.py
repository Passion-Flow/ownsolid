"""Validate retained real Node CLI-test summaries without rerunning their cases."""
import re

def parse_node_summary(text, command_exit):
 if command_exit!=0:raise ValueError('The actual CLI command did not exit zero')
 if re.search(r'^Traceback \(most recent call last\):|^\s*not ok\b|^\s*[\u2716\u2718]',text,re.MULTILINE):raise ValueError('Abnormal or failed test output')
 expected={'tests':7,'pass':7,'fail':0,'cancelled':0,'skipped':0,'todo':0};found={};styles=set()
 for match in re.finditer(r'^(#|\u2139)\s+(tests|pass|fail|cancelled|skipped|todo)\s+(\d+)\s*$',text,re.MULTILINE):
  style,key,value=match.groups()
  if key in found:raise ValueError('Duplicate summary counter: '+key)
  styles.add(style);found[key]=int(value)
 if found!=expected or len(styles)!=1:raise ValueError('Missing, contradictory or failing summary counters')
 style=next(iter(styles))
 if style=='\u2139':
  cases=re.findall(r'^\u2714\s+(.+?)\s+\([\d.]+ms\)\s*$',text,re.MULTILINE)
  if len(cases)!=7 or len(set(cases))!=7:raise ValueError('Expected seven distinct actual passed cases')
 else:
  cases=re.findall(r'^ok\s+\d+\s+-\s+(.+)$',text,re.MULTILINE)
  if len(cases)!=7 or len(set(cases))!=7:raise ValueError('Expected seven distinct TAP passed cases')
 return {'reporter':'spec' if style=='\u2139' else 'tap','metrics':found,'cases':cases}
