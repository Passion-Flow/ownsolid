"""Actual frozen Qwen tokenization with completion-only labels and explicit refusal of truncation."""
from dataclasses import dataclass
from pathlib import Path
from projectone_data.validation import DataError, Limits, inspect_file


class TrainingError(Exception):
    def __init__(self, code, partition=None, line_number=None, cause=None):
        self.code = code
        self.partition = partition
        self.line_number = line_number
        self.cause = cause
        super().__init__(code)


@dataclass(frozen=True)
class Tokenized:
    samples: list[dict[str, list[int]]]
    provenance: list[dict]
    source_sha256: str

    def __len__(self):
        return len(self.samples)

    def __getitem__(self, index):
        return self.samples[index]


def load_training_tokenizer(model_directory: Path):
    """Load the tokenizer for an already verified local model directory."""
    from transformers import AutoTokenizer
    return AutoTokenizer.from_pretrained(model_directory, local_files_only=True, trust_remote_code=False)


def apply_training_template(tokenizer, prompt: str, *, tokenize: bool):
    """Apply the worker's chat template with the assistant generation prefix."""
    return tokenizer.apply_chat_template([{'role': 'user', 'content': prompt}], tokenize=tokenize,
                                         add_generation_prompt=True, enable_thinking=False, return_dict=False)


def training_tokenizer_tokens(tokenizer):
    """Read the special tokens and validate the EOS and padding IDs."""
    special = tuple(value for value in tokenizer.all_special_tokens if value)
    eos = tokenizer.eos_token_id
    if type(eos) is not int or eos < 0 or tokenizer.pad_token_id is None:
        raise TrainingError('TRAINING_TOKENIZER_INVALID')
    return special, eos


def encode_training_row(row, tokenizer, special, eos, partition):
    """Encode the prompt prefix and completion with EOS without truncation."""
    if any(token in row.prompt or token in row.completion for token in special):
        raise TrainingError('TRAINING_RESERVED_TOKEN', partition, row.line_number)
    prefix = apply_training_template(tokenizer, row.prompt, tokenize=True)
    if not isinstance(prefix, list) or not prefix or any(type(token) is not int for token in prefix):
        raise TrainingError('TRAINING_TOKENIZER_INVALID')
    completion = tokenizer.encode(row.completion, add_special_tokens=False)
    if not completion:
        raise TrainingError('TRAINING_EMPTY_TARGET', partition, row.line_number)
    return prefix, completion + [eos]


def tokenize_file(path: Path, expected_sha256: str, tokenizer, limits: Limits, maximum_tokens: int, partition: str):
    try:
        inspected = inspect_file(path, limits)
    except DataError as error:
        raise TrainingError('TRAINING_DATA_INVALID', partition, error.line_number, error.code) from None
    if inspected.source_sha256 != expected_sha256:
        raise TrainingError('TRAINING_INPUT_CHANGED', partition)
    if not inspected.valid():
        error = inspected.errors[0]
        raise TrainingError('TRAINING_DATA_INVALID', partition, error.line_number, error.code)
    samples = []
    provenance = []
    special, eos = training_tokenizer_tokens(tokenizer)
    for row in inspected.rows:
        prefix, completion = encode_training_row(row, tokenizer, special, eos, partition)
        ids = prefix + completion
        if len(ids) > maximum_tokens:
            raise TrainingError('TRAINING_SEQUENCE_TOO_LONG', partition, row.line_number)
        samples.append({'input_ids': ids, 'attention_mask': [1] * len(ids),
                        'labels': [-100] * len(prefix) + completion})
        provenance.append({'line_number': row.line_number, 'content_sha256': row.content_hash(),
                           'prompt_tokens': len(prefix), 'completion_tokens': len(completion)})
    return Tokenized(samples, provenance, inspected.source_sha256)


class CompletionCollator:
    def __init__(self, pad_token_id):
        self.pad_token_id = pad_token_id

    def __call__(self, features):
        import torch
        width = max(len(value['input_ids']) for value in features)
        padded = {key: [] for key in ['input_ids', 'attention_mask', 'labels']}
        for value in features:
            missing = width - len(value['input_ids'])
            padded['input_ids'].append(value['input_ids'] + [self.pad_token_id] * missing)
            padded['attention_mask'].append(value['attention_mask'] + [0] * missing)
            padded['labels'].append(value['labels'] + [-100] * missing)
        return {key: torch.tensor(value, dtype=torch.long) for key, value in padded.items()}
