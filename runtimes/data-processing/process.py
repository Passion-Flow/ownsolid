"""Execute one protected local data attempt; Go independently authorizes and commits business publication."""
import argparse
from dataclasses import asdict
import json
import os
from pathlib import Path
import signal
import sys
sys.dont_write_bytecode=True
sys.path.insert(0,str(Path(__file__).resolve().parent))
from projectone_data import VERSION
from projectone_data.boundary import read_request,read_near_plan,apply_limits,write_result
from projectone_data.validation import DataError,inspect_file
from projectone_data.publication import materialize


def main():
 parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('--request-file',required=True,type=Path);args=parser.parse_args()
 request=None;output=None;maximum=None;format_binding={}
 try:
  request,limits,source,output=read_request(args.request_file);maximum=request['resources']['maximum_result_bytes'];enforcement=apply_limits(request['resources'])
  if 'source_row_format' in request:
   format_binding = {'source_row_format': request['source_row_format']}
  inspected=inspect_file(source,limits,**format_binding)
  if inspected.source_sha256!=request['input_sha256']:raise DataError('DATASET_SOURCE_CHANGED')
  result={'schema_version':1,'processor_version':VERSION,'job_id':request['job_id'],'attempt_id':request['attempt_id'],'operation':request['operation'],'source_sha256':inspected.source_sha256,'source_bytes':inspected.source_bytes,'memory_enforcement':enforcement,**format_binding}
  if request['operation']=='inspect':
   result.update(status='valid' if inspected.valid() else 'invalid',total_rows=inspected.total_rows,invalid_rows=inspected.invalid_rows,errors_truncated=inspected.errors_truncated,fatal_source_error=inspected.fatal_source_error,rows=[asdict(row) for row in inspected.rows],errors=[asdict(error) for error in inspected.errors],**({'fatal_source_line_number':inspected.fatal_source_line_number} if inspected.fatal_source_line_number is not None else {}))
  else:
   if not inspected.valid():raise DataError('DATASET_VALIDATION_FAILED')
   near_plan=read_near_plan(request,limits.maximum_rows)
   artifacts=output/'artifacts';artifacts.mkdir(mode=0o700)
   result.update(status='prepared',manifest=materialize(inspected.rows,artifacts,request['seed'],request['validation_basis_points'],inspected.source_sha256,near_plan,test_basis_points=request.get('test_basis_points')))
  write_result(output,result,maximum);print(json.dumps({'status':'prepared' if request['operation']=='publish' else result['status'],'job_id':request['job_id'],'attempt_id':request['attempt_id']}));return 0
 except (DataError,OSError,ValueError,TypeError,MemoryError,RecursionError) as error:
  code=error.code if isinstance(error,DataError) else 'DATASET_PROCESSOR_MEMORY_EXCEEDED' if isinstance(error,MemoryError) else 'DATASET_PROCESSOR_FAILED'
  if output is not None and request is not None and maximum is not None:
   try:write_result(output,{'schema_version':1,'status':'failed','processor_version':VERSION,'job_id':request['job_id'],'attempt_id':request['attempt_id'],'operation':request['operation'],'error_code':code,**format_binding,**({'error_line_number':error.line_number} if isinstance(error,DataError) and error.line_number is not None else {})},maximum)
   except (DataError,OSError,MemoryError):pass
  print(json.dumps({'status':'failed','code':code}));return 1
 finally:signal.alarm(0)

if __name__=='__main__':sys.exit(main())
