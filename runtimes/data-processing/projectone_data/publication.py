"""Exact deduplication and deterministic source-connected group split; immutable files only."""
import hashlib
import json
import os
from pathlib import Path
import re
from . import VERSION,SCHEMA,SPLIT_ALGORITHM
from .validation import DataError,Row,compact

SPLIT_DOMAIN=b'projectone.text-sft.v1.split.v1\x00'

class Sources:
 def __init__(self):self.parents={}
 def find(self,value):
  self.parents.setdefault(value,value)
  current=value
  while self.parents[current]!=current:current=self.parents[current]
  while self.parents[value]!=value:
   parent=self.parents[value];self.parents[value]=current;value=parent
  return current
 def union(self,a,b):
  a=self.find(a);b=self.find(b)
  if a!=b:self.parents[max(a,b)]=min(a,b)

def split(rows:list[Row],seed:str,validation_basis_points:int,near_plan=None):
 if not re.fullmatch('[a-f0-9]{32}',seed) or type(validation_basis_points) is not int or not 100<=validation_basis_points<=5000:raise DataError('DATASET_SPLIT_CONFIGURATION_INVALID')
 if not rows:raise DataError('DATASET_EMPTY_SOURCE')
 if near_plan is not None and len(near_plan)!=len(rows):raise DataError('DATASET_PROCESSOR_REQUEST_INVALID')
 sources=Sources();retained={};duplicates=[]
 for row in rows:
  sources.find(row.source_id);fingerprint=row.content_hash()
  if near_plan is not None:
   entry=near_plan[row.line_number-1]
   sources.union(row.source_id,rows[entry['group']-1].source_id)
   if entry['match']:
    duplicates.append({'line_number':row.line_number,'source_id':row.source_id,'kept_line_number':entry['match'],'content_sha256':fingerprint,'match_rule':'near_prompt_v1'})
    continue
  if fingerprint in retained:
   first=retained[fingerprint]
   # Union before dropping content duplicates, including transitive source bridges.
   sources.union(first.source_id,row.source_id)
   duplicates.append({'line_number':row.line_number,'source_id':row.source_id,'kept_line_number':first.line_number,'content_sha256':fingerprint})
  else:retained[fingerprint]=row
 groups={}
 for source in sorted(sources.parents):groups.setdefault(sources.find(source),[]).append(source)
 if len(groups)<2:raise DataError('DATASET_SPLIT_INSUFFICIENT_GROUPS')
 ordered=sorted(groups,key=lambda root:(hashlib.sha256(SPLIT_DOMAIN+seed.encode('ascii')+b'\x00'+compact(groups[root])).hexdigest(),compact(groups[root])))
 count=min(len(groups)-1,max(1,(len(groups)*validation_basis_points+5000)//10000));validation_roots=set(ordered[:count])
 train=[];validation=[];assignments=[]
 for fingerprint,row in retained.items():
  partition='validation' if sources.find(row.source_id) in validation_roots else 'train'
  (validation if partition=='validation' else train).append(row)
  assignments.append({'line_number':row.line_number,'source_id':row.source_id,'content_sha256':fingerprint,'split':partition})
 if not train or not validation:raise DataError('DATASET_SPLIT_INSUFFICIENT_GROUPS')
 return {'train':train,'validation':validation,'samples':list(retained.values()),'assignments':assignments,'duplicates':duplicates,'groups':groups,'validation_groups':count,'seed':seed,'validation_basis_points':validation_basis_points}

def write_immutable(path:Path,lines):
 digest=hashlib.sha256();size=0
 with path.open('xb') as output:
  os.chmod(output.fileno(),0o600)
  for value in lines:
   content=compact(value)+b'\n';output.write(content);digest.update(content);size+=len(content)
  output.flush();os.fsync(output.fileno())
 return {'path':path.name,'size_bytes':size,'sha256':digest.hexdigest()}

def materialize(rows:list[Row],directory:Path,seed:str,validation_basis_points:int,source_sha256:str,near_plan=None):
 if not re.fullmatch('[a-f0-9]{64}',source_sha256):raise DataError('DATASET_SOURCE_DIGEST_INVALID')
 result=split(rows,seed,validation_basis_points,near_plan)
 if directory.is_symlink() or not directory.is_dir() or list(directory.iterdir()):raise DataError('DATASET_OUTPUT_CONFLICT')
 files=[]
 for name in ['samples','train','validation']:files.append(write_immutable(directory/(name+'.jsonl'),(row.value() for row in result[name])))
 files.append(write_immutable(directory/'assignments.jsonl',result['assignments']));files.append(write_immutable(directory/'duplicates.jsonl',result['duplicates']))
 manifest={'schema_version':1,'row_schema':SCHEMA,'processor_version':VERSION,'split_algorithm':SPLIT_ALGORITHM,'source_sha256':source_sha256,'seed':seed,'validation_basis_points':validation_basis_points,'total_rows':len(rows),'unique_rows':len(result['samples']),'duplicate_rows':len(result['duplicates']),'source_groups':len(result['groups']),'validation_groups':result['validation_groups'],'train_rows':len(result['train']),'validation_rows':len(result['validation']),'files':files}
 manifest_file=write_immutable(directory/'manifest.json',[manifest])
 descriptor=os.open(directory,os.O_RDONLY)
 try:os.fsync(descriptor)
 finally:os.close(descriptor)
 return {**manifest,'manifest_file':manifest_file}
