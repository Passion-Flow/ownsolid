"""Bounded strict JSONL validation. Raw source bytes are never executed or silently repaired."""
from dataclasses import dataclass
import hashlib
import json
from pathlib import Path
import unicodedata
from . import SINGLE_TURN_MESSAGES_V1
from .limits_generated import LIMIT_CEILINGS

class DataError(Exception):
 def __init__(self,code,line_number=None):self.code=code;self.line_number=line_number;super().__init__(code)

@dataclass(frozen=True)
class Limits:
 maximum_bytes:int
 maximum_rows:int
 maximum_line_bytes:int
 maximum_text_bytes:int
 maximum_source_bytes:int
 maximum_errors:int

 def __post_init__(self):
  ceilings=LIMIT_CEILINGS
  for key,(low,high) in ceilings.items():
   value=getattr(self,key)
   if type(value) is not int or not low<=value<=high:raise DataError('DATASET_PROCESSOR_LIMITS_INVALID')
  if self.maximum_line_bytes>self.maximum_bytes or self.maximum_text_bytes*2>self.maximum_line_bytes or self.maximum_errors>self.maximum_rows:raise DataError('DATASET_PROCESSOR_LIMITS_INVALID')

@dataclass(frozen=True)
class Row:
 line_number:int
 source_id:str
 prompt:str
 completion:str

 def value(self):return {'source_id':self.source_id,'prompt':self.prompt,'completion':self.completion}
 def content_hash(self):return hashlib.sha256(compact([self.prompt,self.completion])).hexdigest()

@dataclass(frozen=True)
class LineError:
 line_number:int
 code:str
 preview:str
 preview_replaced:bool

@dataclass(frozen=True)
class Inspection:
 rows:list[Row]
 errors:list[LineError]
 total_rows:int
 invalid_rows:int
 errors_truncated:bool
 source_sha256:str
 source_bytes:int
 fatal_source_error:bool
 fatal_source_line_number:int|None=None

 def valid(self):return self.invalid_rows==0 and self.total_rows>0

# Versioned exact UTF-8 representation; field order for records is source_id/prompt/completion.
def compact(value):return json.dumps(value,ensure_ascii=False,separators=(',',':'),allow_nan=False).encode('utf-8')

def unique(pairs):
 out={}
 for key,value in pairs:
  if key in out:raise DataError('DATASET_DUPLICATE_FIELD')
  out[key]=value
 return out

def invalid_constant(value):raise DataError('DATASET_JSON_INVALID')

def valid_text(value,maximum,source=False):
 if not isinstance(value,str) or not value.strip():return False
 try:encoded=value.encode('utf-8',errors='strict')
 except UnicodeError:return False
 if len(encoded)>maximum:return False
 if source and value!=value.strip():return False
 for character in value:
  if unicodedata.category(character)=='Cc' and (source or character not in '\t\r\n'):return False
 return True

def parse_line(raw,line_number,limits,*,source_row_format=None):
 if len(raw)>limits.maximum_line_bytes:raise DataError('DATASET_LINE_TOO_LARGE')
 try:text=raw.decode('utf-8',errors='strict')
 except UnicodeError:raise DataError('DATASET_ENCODING_INVALID') from None
 if line_number==1 and text.startswith('\ufeff'):raise DataError('DATASET_BOM_FORBIDDEN')
 if not text.strip():raise DataError('DATASET_EMPTY_LINE')
 try:value=json.loads(text,object_pairs_hook=unique,parse_constant=invalid_constant)
 except DataError:raise
 except (ValueError,RecursionError):raise DataError('DATASET_JSON_INVALID') from None
 source_id, prompt, completion = row_contents(value, source_row_format)
 if not valid_text(source_id, limits.maximum_source_bytes, True):
  raise DataError('DATASET_SOURCE_INVALID')
 if not all(valid_text(content, limits.maximum_text_bytes) for content in (prompt, completion)):
  raise DataError('DATASET_TEXT_INVALID')
 return Row(line_number, source_id, prompt, completion)


def row_contents(value, source_row_format):
 if source_row_format is None:
  if not isinstance(value, dict) or set(value) != {'source_id', 'prompt', 'completion'}:
   raise DataError('DATASET_FIELDS_INVALID')
  return value['source_id'], value['prompt'], value['completion']
 if source_row_format != SINGLE_TURN_MESSAGES_V1:
  raise DataError('DATASET_PROCESSOR_REQUEST_INVALID')
 if not isinstance(value, dict) or set(value) != {'source_id', 'messages'}:
  raise DataError('DATASET_FIELDS_INVALID')
 messages = value['messages']
 if not isinstance(messages, list) or len(messages) != 2:
  raise DataError('DATASET_MESSAGE_SEQUENCE_INVALID')
 for message, role in zip(messages, ('user', 'assistant'), strict=True):
  if not isinstance(message, dict) or set(message) != {'role', 'content'}:
   raise DataError('DATASET_FIELDS_INVALID')
  if message['role'] != role:
   raise DataError('DATASET_MESSAGE_SEQUENCE_INVALID')
 return value['source_id'], messages[0]['content'], messages[1]['content']

def preview(raw):
 try:text=raw[:256].decode('utf-8',errors='strict');replaced=False
 except UnicodeError:text=raw[:256].decode('utf-8',errors='replace');replaced=True
 # Escaped preview is labeled; original immutable bytes/hash remain the authority.
 return json.dumps(text,ensure_ascii=False)[1:-1],replaced

def inspect_file(path:Path,limits:Limits,*,source_row_format=None):
 if path.stat().st_size>limits.maximum_bytes:raise DataError('DATASET_FILE_TOO_LARGE')
 rows=[];errors=[];total=0;invalid=0;size=0;digest=hashlib.sha256();fatal=False;fatal_line=None
 def consume(block):
  nonlocal size
  size+=len(block)
  if size>limits.maximum_bytes:raise DataError('DATASET_FILE_TOO_LARGE')
  digest.update(block)
 def error(raw,line,code):
  nonlocal invalid,fatal,fatal_line
  invalid+=1
  if code in {'DATASET_ENCODING_INVALID','DATASET_BOM_FORBIDDEN'}:
   fatal=True
   if fatal_line is None:fatal_line=line
  if len(errors)<limits.maximum_errors:
   escaped,replaced=preview(raw);errors.append(LineError(line,code,escaped,replaced))
 with path.open('rb') as source:
  while True:
   raw=source.readline(limits.maximum_line_bytes+3)
   if not raw:break
   consume(raw);total+=1
   if total>limits.maximum_rows:raise DataError('DATASET_ROW_LIMIT',total)
   if not raw.endswith(b'\n') and len(raw)>limits.maximum_line_bytes:
    first=raw
    while raw and not raw.endswith(b'\n'):
     raw=source.readline(limits.maximum_line_bytes+3);consume(raw)
    error(first,total,'DATASET_LINE_TOO_LARGE');continue
   content=raw[:-1] if raw.endswith(b'\n') else raw
   if content.endswith(b'\r'):content=content[:-1]
   try:rows.append(parse_line(content,total,limits,source_row_format=source_row_format))
   except DataError as e:error(content,total,e.code)
 if total==0:error(b'',0,'DATASET_EMPTY_SOURCE')
 return Inspection(rows,errors,total,invalid,invalid>len(errors),digest.hexdigest(),size,fatal,fatal_line)
