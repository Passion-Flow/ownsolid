"""Encode all source tokens with the pinned local BERT model and exact UTF-8 offsets."""
import hashlib

from .boundary import fail
from .contract_generated import CONTRACT, RUNTIME
import importlib.metadata
import platform
import sys


def verify_runtime():
    profiles = [value for value in RUNTIME['profiles'].values() if value['system'] == sys.platform and value['machine'] == platform.machine()]
    if len(profiles) != 1 or platform.python_version() != RUNTIME['python']:
        fail('KNOWLEDGE_ENCODER_LOAD_FAILED')
    try:
        if any(importlib.metadata.version(name) != version for name, version in profiles[0]['packages'].items()):
            fail('KNOWLEDGE_ENCODER_LOAD_FAILED')
    except importlib.metadata.PackageNotFoundError:
        fail('KNOWLEDGE_ENCODER_LOAD_FAILED')


def encode(request):
    verify_runtime()
    try:
        import torch
        from transformers import AutoModel, AutoTokenizer
        from transformers.utils import logging
        logging.set_verbosity_error()
        logging.disable_progress_bar()
        torch.set_num_threads(request['resources']['threads'])
        torch.set_num_interop_threads(1)
        tokenizer = AutoTokenizer.from_pretrained(request['model_directory'], local_files_only=True, trust_remote_code=False, use_fast=True)
        if not tokenizer.is_fast or tokenizer.num_special_tokens_to_add(pair=False) != CONTRACT['maximum_sequence_tokens'] - CONTRACT['maximum_chunk_tokens']:
            fail('KNOWLEDGE_ENCODER_ASSET_INVALID')
        model = AutoModel.from_pretrained(request['model_directory'], local_files_only=True, trust_remote_code=False, use_safetensors=True, dtype=torch.float32, attn_implementation='eager').to('cpu').eval()
        if model.config.hidden_size != CONTRACT['dimension']:
            fail('KNOWLEDGE_ENCODER_ASSET_INVALID')
    except ImportError:
        fail('KNOWLEDGE_ENCODER_LOAD_FAILED')
    text, limits = request['text'], request['limits']
    tokens = tokenizer(text, add_special_tokens=False, return_offsets_mapping=True, return_attention_mask=False, return_token_type_ids=False, truncation=False, padding=False)
    ids, offsets = tokens['input_ids'], tokens['offset_mapping']
    if not ids:
        fail('KNOWLEDGE_EMPTY_SOURCE')
    if len(ids) > limits['source_tokens']:
        fail('KNOWLEDGE_SOURCE_LIMIT_EXCEEDED')
    if request['operation'] == 'query' and len(ids) > limits['query_tokens']:
        fail('KNOWLEDGE_QUERY_TOO_LONG')
    byte_positions = [0]
    for char in text:
        byte_positions.append(byte_positions[-1] + len(char.encode('utf-8')))
    windows = []
    step = limits['chunk_tokens'] - limits['overlap_tokens']
    for start in range(0, len(ids), step if request['operation'] == 'document' else len(ids)):
        end = min(len(ids), start + limits['chunk_tokens']) if request['operation'] == 'document' else len(ids)
        if len(windows) >= limits['chunks']:
            fail('KNOWLEDGE_SOURCE_LIMIT_EXCEEDED')
        first, last = offsets[start][0], offsets[end-1][1]
        if not 0 <= first < last <= len(text):
            fail('KNOWLEDGE_WORKER_RESULT_INVALID')
        start_byte = 0 if start == 0 else byte_positions[first]
        end_byte = byte_positions[-1] if end == len(ids) else byte_positions[last]
        if end_byte-start_byte > limits['chunk_bytes']:
            fail('KNOWLEDGE_SOURCE_LIMIT_EXCEEDED')
        windows.append({'start_byte': start_byte, 'end_byte': end_byte, 'token_count': end - start, 'ids': ids[start:end]})
        if end == len(ids):
            break
    output = []
    batch_size = request['resources']['batch_size']
    with torch.inference_mode():
        for begin in range(0, len(windows), batch_size):
            group = windows[begin:begin+batch_size]
            # Preserve original WordPiece IDs across windows, including continuation pieces.
            # The approved BERT template is CLS + tokens + SEP, with zero type IDs.
            if any(type(value) is not int for value in [tokenizer.cls_token_id, tokenizer.sep_token_id, tokenizer.pad_token_id]):
                fail('KNOWLEDGE_ENCODER_ASSET_INVALID')
            sequences = [[tokenizer.cls_token_id] + item['ids'] + [tokenizer.sep_token_id] for item in group]
            width = max(len(sequence) for sequence in sequences)
            features = {
                'input_ids': torch.tensor([sequence + [tokenizer.pad_token_id] * (width-len(sequence)) for sequence in sequences], dtype=torch.long),
                'attention_mask': torch.tensor([[1] * len(sequence) + [0] * (width-len(sequence)) for sequence in sequences], dtype=torch.long),
                'token_type_ids': torch.zeros((len(sequences), width), dtype=torch.long),
            }
            if features['input_ids'].shape[1] > CONTRACT['maximum_sequence_tokens']:
                fail('KNOWLEDGE_WORKER_RESULT_INVALID')
            hidden = model(**features).last_hidden_state
            mask = features['attention_mask'].unsqueeze(-1).to(hidden.dtype)
            pooled = (hidden * mask).sum(dim=1) / mask.sum(dim=1).clamp(min=1e-9)
            vectors = torch.nn.functional.normalize(pooled, p=2, dim=1)
            if not torch.isfinite(vectors).all():
                fail('KNOWLEDGE_WORKER_RESULT_INVALID')
            for item, vector in zip(group, vectors.tolist()):
                output.append({'ordinal': len(output), 'start_byte': item['start_byte'], 'end_byte': item['end_byte'], 'token_count': item['token_count'], 'embedding': vector})
    return {'schema_version': CONTRACT['schema_version'], 'encoder_catalog_sha256': CONTRACT['encoder_catalog_sha256'], 'dimension': CONTRACT['dimension'], 'source_sha256': hashlib.sha256(text.encode('utf-8')).hexdigest(), 'chunks': output}
