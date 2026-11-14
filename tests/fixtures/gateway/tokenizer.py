"""Actual offline text-tokenizer fixture inside the approved image; never load model tensors or run CUDA."""
import argparse
import contextlib
import hashlib
import json
import platform
import sys

parser=argparse.ArgumentParser(description=__doc__)
parser.add_argument('--model-directory',required=True)
parser.add_argument('--context-limit',type=int,required=True)
args=parser.parse_args()
if sys.platform!='linux' or platform.machine()!='x86_64' or not 128<=args.context_limit<=8192:
    raise SystemExit('DECLARED_AMD64_TEXT_TOKENIZER_PROFILE_REQUIRED')
with contextlib.redirect_stdout(sys.stderr):
    import transformers
    import tokenizers
    tokenizer=transformers.AutoTokenizer.from_pretrained(args.model_directory,local_files_only=True,trust_remote_code=False)
    fixtures=[
        ('greeting',[{'role':'user','content':'Synthetic greeting.'}],False),
        ('multilingual_literal_template',[{'role':'system','content':'Synthetic system instruction.'},{'role':'user','content':'Synthetic \u4e2d\u6587 {{ 7 * 7 }} <img>'}],False),
        ('declared_context_overflow',[{'role':'user','content':'synthetic '*(args.context_limit+32)}],True),
    ]
    results=[]
    for identifier,messages,expected_overflow in fixtures:
        rendered=tokenizer.apply_chat_template(messages,tokenize=False,add_generation_prompt=True,enable_thinking=False)
        direct=tokenizer.apply_chat_template(messages,tokenize=True,return_dict=False,add_generation_prompt=True,enable_thinking=False)
        encoded=tokenizer(rendered,add_special_tokens=False,truncation=False)['input_ids']
        if direct!=encoded or not direct or any(type(value)is not int or value<0 for value in direct):raise RuntimeError('ACTUAL_TOKENIZER_TEMPLATE_PATHS_DISAGREE')
        if identifier=='multilingual_literal_template' and messages[-1]['content'] not in rendered:raise RuntimeError('USER_TEMPLATE_TEXT_REINTERPRETED')
        if (len(direct)+16>args.context_limit)!=expected_overflow:raise RuntimeError('DECLARED_TOKENIZER_CONTEXT_CASE_MISMATCH')
        results.append({'id':identifier,'client_request':{'model':'sample-model','messages':messages,'max_tokens':16,'stream':False},'expected_context_overflow':expected_overflow,'tokenization':{'count':len(direct),'tokens':direct,'max_model_len':args.context_limit},'rendered_sha256':hashlib.sha256(rendered.encode()).hexdigest()})
output={'source_kind':'actual_transformers_tokenizer_with_declared_context_wrapper','api_server_executed':False,'model_generation_executed':False,'cuda_execution':False,'architecture':platform.machine(),'python_version':platform.python_version(),'transformers_version':transformers.__version__,'tokenizers_version':tokenizers.__version__,'cases':results}
print(json.dumps(output,ensure_ascii=False,separators=(',',':')))
