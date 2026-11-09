"""Protected local request boundary and explicit process limits for the owned data worker."""
import hashlib
import json
import os
from pathlib import Path
import re
import resource
import signal
import stat
import sys
import uuid
from .validation import DataError,Limits,unique,invalid_constant
from .limits_generated import RESOURCE_CEILINGS

MAX_REQUEST_BYTES=16384
MAX_OPEN_FILES=64


def private_directory(value,scope=None):
 if not isinstance(value,str):raise DataError('DATASET_PROCESSOR_PATH_INVALID')
 path=Path(value)
 try:
  info=path.lstat()
  if not path.is_absolute() or path.resolve(strict=True)!=path or not stat.S_ISDIR(info.st_mode) or info.st_uid!=os.geteuid() or info.st_mode&0o077 or scope is not None and (path==scope or not path.is_relative_to(scope)):raise ValueError()
 except (OSError,ValueError):raise DataError('DATASET_PROCESSOR_PATH_INVALID') from None
 if scope is not None:
  for parent in path.parents:
   if parent==scope:break
   info=parent.lstat()
   if parent.is_symlink() or not stat.S_ISDIR(info.st_mode) or info.st_uid!=os.geteuid() or info.st_mode&0o077:raise DataError('DATASET_PROCESSOR_PATH_INVALID')
 return path


def private_file(value,maximum,scope=None):
 if not isinstance(value,str):raise DataError('DATASET_PROCESSOR_PATH_INVALID')
 path=Path(value)
 try:
  info=path.lstat()
  if not path.is_absolute() or path.resolve(strict=True)!=path or not stat.S_ISREG(info.st_mode) or info.st_uid!=os.geteuid() or info.st_mode&0o077 or info.st_size>maximum or scope is not None and not path.is_relative_to(scope):raise ValueError()
 except (OSError,ValueError):raise DataError('DATASET_PROCESSOR_PATH_INVALID') from None
 if scope is not None:private_directory(str(path.parent),scope) if path.parent!=scope else None
 return path


def exact(value,fields):
 if not isinstance(value,dict) or set(value)!=set(fields):raise DataError('DATASET_PROCESSOR_REQUEST_INVALID')


def valid_id(value):
 try:return isinstance(value,str) and str(uuid.UUID(value))==value and uuid.UUID(value).int!=0
 except ValueError:return False


def read_request(path):
 file=private_file(str(path),MAX_REQUEST_BYTES)
 try:value=json.loads(file.read_bytes().decode('utf-8',errors='strict'),object_pairs_hook=unique,parse_constant=invalid_constant)
 except (ValueError,UnicodeError,RecursionError):raise DataError('DATASET_PROCESSOR_REQUEST_INVALID') from None
 if not isinstance(value,dict):raise DataError('DATASET_PROCESSOR_REQUEST_INVALID')
 fields={'schema_version','operation','job_id','attempt_id','scope_directory','input_file','input_sha256','output_directory','limits','resources'}
 if value.get('operation')=='publish':
  fields|={'seed','validation_basis_points'}
  if 'test_basis_points' in value:
   fields.add('test_basis_points')
   if type(value['test_basis_points']) is not int or 'near_duplicate_plan_sha256' not in value:
    raise DataError('DATASET_PROCESSOR_REQUEST_INVALID')
  if 'near_duplicate_plan_sha256' in value:
   fields.add('near_duplicate_plan_sha256')
   plan_digest=value['near_duplicate_plan_sha256']
   if not isinstance(plan_digest,str) or not re.fullmatch('[a-f0-9]{64}',plan_digest):raise DataError('DATASET_PROCESSOR_REQUEST_INVALID')
 exact(value,fields)
 if type(value['schema_version']) is not int or value['schema_version']!=1 or value['operation'] not in {'inspect','publish'} or not valid_id(value['job_id']) or not valid_id(value['attempt_id']):raise DataError('DATASET_PROCESSOR_REQUEST_INVALID')
 scope=private_directory(value['scope_directory']);private_file(str(file),MAX_REQUEST_BYTES,scope)
 exact(value['limits'],Limits.__dataclass_fields__)
 try:limits=Limits(**value['limits'])
 except TypeError:raise DataError('DATASET_PROCESSOR_LIMITS_INVALID') from None
 source=private_file(value['input_file'],limits.maximum_bytes,scope)
 output=private_directory(value['output_directory'],scope)
 if list(output.iterdir()) or file.is_relative_to(output) or source.is_relative_to(output):raise DataError('DATASET_OUTPUT_CONFLICT')
 if not isinstance(value['input_sha256'],str) or not re.fullmatch('[a-f0-9]{64}',value['input_sha256']):raise DataError('DATASET_SOURCE_DIGEST_INVALID')
 exact(value['resources'],{'wall_seconds','cpu_seconds','memory_bytes','maximum_result_bytes'})
 bounds=RESOURCE_CEILINGS
 for name,(minimum,maximum) in bounds.items():
  if type(value['resources'][name]) is not int or not minimum<=value['resources'][name]<=maximum:raise DataError('DATASET_PROCESSOR_RESOURCES_INVALID')
 if value['resources']['wall_seconds']<value['resources']['cpu_seconds']:raise DataError('DATASET_PROCESSOR_RESOURCES_INVALID')
 return value,limits,source,output


def read_near_plan(request, maximum_rows):
 """Read the Go-owned plan from the exact attempt and verify its frozen digest."""
 expected = request.get('near_duplicate_plan_sha256')
 if expected is None:
  return None
 scope = Path(request['scope_directory'])
 maximum_bytes = maximum_rows * 64
 path = private_file(str(scope / 'near-duplicates.json'), maximum_bytes, scope)
 with path.open('rb') as plan_file:
  raw = plan_file.read(maximum_bytes + 1)
 if len(raw) > maximum_bytes or hashlib.sha256(raw).hexdigest() != expected:
  raise DataError('DATASET_PROCESSOR_REQUEST_INVALID')
 try:
  plan = json.loads(raw.decode('utf-8'), object_pairs_hook=unique, parse_constant=invalid_constant)
 except (ValueError, UnicodeError, RecursionError):
  raise DataError('DATASET_PROCESSOR_REQUEST_INVALID') from None
 if not isinstance(plan, list) or not 0 < len(plan) <= maximum_rows:
  raise DataError('DATASET_PROCESSOR_REQUEST_INVALID')
 for line, entry in enumerate(plan, 1):
  exact(entry, {'group', 'match'})
  group, match = entry['group'], entry['match']
  if type(group) is not int or not 1 <= group <= line or type(match) is not int or not 0 <= match < line:
   raise DataError('DATASET_PROCESSOR_REQUEST_INVALID')
  if plan[group - 1]['group'] != group or match and (
      plan[match - 1]['group'] != group or plan[match - 1]['match'] != 0
  ):
   raise DataError('DATASET_PROCESSOR_REQUEST_INVALID')
 return plan


def apply_limits(values):
 def timeout(signum,frame):raise DataError('DATASET_PROCESSOR_TIMEOUT')
 signal.signal(signal.SIGALRM,timeout);signal.alarm(values['wall_seconds'])
 signal.signal(signal.SIGXCPU,timeout)
 resource.setrlimit(resource.RLIMIT_CPU,(values['cpu_seconds'],values['cpu_seconds']+1))
 resource.setrlimit(resource.RLIMIT_FSIZE,(values['maximum_result_bytes'],values['maximum_result_bytes']))
 resource.setrlimit(resource.RLIMIT_NOFILE,(MAX_OPEN_FILES,MAX_OPEN_FILES))
 if sys.platform=='linux':
  resource.setrlimit(resource.RLIMIT_AS,(values['memory_bytes'],values['memory_bytes']))
  return 'linux_rlimit_as_cpu_fsize_wall'
 return 'native_cpu_fsize_wall_input_output_bounds_parent_RSS_required'


def write_result(directory,value,maximum):
 data=json.dumps(value,ensure_ascii=False,allow_nan=False,separators=(',',':')).encode('utf-8')+b'\n'
 if len(data)>maximum:raise DataError('DATASET_PROCESSOR_RESULT_TOO_LARGE')
 with (directory/'result.json').open('xb') as output:
  os.chmod(output.fileno(),0o600);output.write(data);output.flush();os.fsync(output.fileno())
 descriptor=os.open(directory,os.O_RDONLY)
 try:os.fsync(descriptor)
 finally:os.close(descriptor)
