"""Bounded offline preview of approved training tokenization."""
from dataclasses import dataclass
import hashlib
import importlib.metadata
import os
from pathlib import Path
import platform
import re
import stat
from tempfile import TemporaryDirectory

from projectone_data.limits_generated import LIMIT_CEILINGS
from projectone_data.validation import DataError, Row, valid_text

from .boundary import read_installed_catalog
from .catalog import catalog_digest
from .configuration import runtime_manifest
from .configuration_generated import CONTRACT
from .data import (
    TrainingError,
    apply_training_template,
    encode_training_row,
    load_training_tokenizer,
    training_tokenizer_tokens,
)

MAXIMUM_SAMPLES = 8
MAXIMUM_SAMPLE_BYTES = 64 * 1024
_MAXIMUM_ASSET_BYTES = 16 * 1024 * 1024
_ASSET_READ_BYTES = 128 * 1024
_MAXIMUM_LOCK_BYTES = 1024 * 1024
_TOKENIZER_FILES = ('config.json', 'merges.txt', 'tokenizer.json', 'tokenizer_config.json', 'vocab.json')
_MODEL_ONLY_FILES = {'LICENSE', 'README.md', 'generation_config.json', 'model.safetensors'}
_TOKENIZER_PACKAGES = {'transformers', 'tokenizers', 'jinja2', 'markupsafe', 'huggingface-hub'}
_RUNTIME_LOCKS = {
    ('Darwin', 'arm64'): 'native-macos-arm64.lock',
    ('Linux', 'x86_64'): 'linux-amd64-cuda.lock',
}
_RUNTIME_ROOT = Path(__file__).resolve().parents[1]


@dataclass(frozen=True)
class SamplePreview:
    """Rendered training sections and their exact token counts for one source row."""

    line_number: int
    content_sha256: str
    rendered_prompt: str
    completion: str
    eos: str
    prompt_tokens: int
    completion_tokens: int
    total_tokens: int
    first_excess_token_index: int | None
    first_excess_section: str | None


@dataclass(frozen=True)
class TokenizerPreview:
    """A preview bound to the approved model catalogue and training token budget."""

    model_catalog_sha256: str
    maximum_tokens: int
    samples: tuple[SamplePreview, ...]


def _approved_assets(expected_sha256):
    try:
        catalog = read_installed_catalog(_RUNTIME_ROOT)
        actual_sha256 = catalog_digest(catalog)
    except (TrainingError, DataError, OSError, ValueError, RecursionError):
        raise TrainingError('TOKENIZER_PREVIEW_CATALOG_UNAVAILABLE') from None
    if type(expected_sha256) is not str or expected_sha256 != actual_sha256:
        raise TrainingError('TOKENIZER_PREVIEW_CATALOG_MISMATCH')
    assets = {entry['path']: entry for entry in catalog['files']}
    if catalog['model_type'] != 'qwen3' or assets.keys() != set(_TOKENIZER_FILES) | _MODEL_ONLY_FILES:
        raise TrainingError('TOKENIZER_PREVIEW_CATALOG_UNSUPPORTED')
    tokenizer_assets = tuple(assets[name] for name in _TOKENIZER_FILES)
    if sum(asset['size_bytes'] for asset in tokenizer_assets) > _MAXIMUM_ASSET_BYTES:
        raise TrainingError('TOKENIZER_PREVIEW_CATALOG_UNSUPPORTED')
    return catalog['max_context'], tokenizer_assets


def _validate_samples(samples):
    if type(samples) not in (list, tuple) or not 1 <= len(samples) <= MAXIMUM_SAMPLES:
        raise TrainingError('TOKENIZER_PREVIEW_SAMPLE_LIMIT')
    remaining_bytes = MAXIMUM_SAMPLE_BYTES
    for row in samples:
        if type(row) is not Row or type(row.line_number) is not int or not 1 <= row.line_number < 2**63:
            raise TrainingError('TOKENIZER_PREVIEW_SAMPLE_INVALID')
        for text, field, source in ((row.source_id, 'maximum_source_bytes', True),
                                    (row.prompt, 'maximum_text_bytes', False),
                                    (row.completion, 'maximum_text_bytes', False)):
            if type(text) is not str:
                raise TrainingError('TOKENIZER_PREVIEW_SAMPLE_INVALID', 'preview', row.line_number)
            maximum = min(remaining_bytes, LIMIT_CEILINGS[field][1])
            if len(text) > maximum:
                raise TrainingError('TOKENIZER_PREVIEW_SAMPLE_LIMIT', 'preview', row.line_number)
            try:
                size = len(text.encode('utf-8'))
            except UnicodeError:
                raise TrainingError('TOKENIZER_PREVIEW_SAMPLE_INVALID', 'preview', row.line_number) from None
            if size > maximum:
                raise TrainingError('TOKENIZER_PREVIEW_SAMPLE_LIMIT', 'preview', row.line_number)
            if not valid_text(text, maximum, source):
                raise TrainingError('TOKENIZER_PREVIEW_SAMPLE_INVALID', 'preview', row.line_number)
            remaining_bytes -= size


def _verify_runtime():
    try:
        manifest = runtime_manifest()
        if platform.python_version_tuple()[:2] != tuple(manifest['python'].split('.')[:2]):
            raise ValueError
        lock_name = _RUNTIME_LOCKS[(platform.system(), platform.machine())]
        lock_path = _RUNTIME_ROOT / 'requirements' / lock_name
        info = lock_path.lstat()
        if not stat.S_ISREG(info.st_mode) or info.st_mode & 0o022 or info.st_size > _MAXIMUM_LOCK_BYTES:
            raise ValueError
        contents = lock_path.read_bytes()
        if hashlib.sha256(contents).hexdigest() != manifest['locks'][lock_name]:
            raise ValueError
        versions = dict(re.findall(r'^([a-z0-9-]+)==([^\s]+)', contents.decode('utf-8'), re.MULTILINE))
        if any(importlib.metadata.version(name) != versions[name] for name in _TOKENIZER_PACKAGES):
            raise ValueError
    except (TrainingError, OSError, ValueError, KeyError, TypeError, RecursionError,
            importlib.metadata.PackageNotFoundError):
        raise TrainingError('TOKENIZER_PREVIEW_RUNTIME_UNAVAILABLE') from None


def _copy_asset(directory_fd, asset, output):
    name = asset['path']
    flags = os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK
    with os.fdopen(os.open(name, flags, dir_fd=directory_fd), 'rb') as source:
        info = os.fstat(source.fileno())
        size = asset['size_bytes']
        if not stat.S_ISREG(info.st_mode) or info.st_mode & 0o022 or info.st_size != size:
            raise TrainingError('TOKENIZER_PREVIEW_ASSET_CHANGED')
        digest = hashlib.sha256()
        with (output / name).open('xb') as target:
            remaining = size
            while remaining:
                block = source.read(min(_ASSET_READ_BYTES, remaining))
                if not block:
                    raise TrainingError('TOKENIZER_PREVIEW_ASSET_CHANGED')
                digest.update(block)
                target.write(block)
                remaining -= len(block)
            if source.read(1) or digest.hexdigest() != asset['sha256']:
                raise TrainingError('TOKENIZER_PREVIEW_ASSET_CHANGED')


def _copy_tokenizer(model_directory, assets, output):
    try:
        if model_directory.resolve(strict=True) != model_directory:
            raise TrainingError('TOKENIZER_PREVIEW_ASSET_UNAVAILABLE')
        directory_fd = os.open(model_directory, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
        try:
            if os.fstat(directory_fd).st_mode & 0o022:
                raise TrainingError('TOKENIZER_PREVIEW_ASSET_UNAVAILABLE')
            for asset in assets:
                _copy_asset(directory_fd, asset, output)
        finally:
            os.close(directory_fd)
    except OSError:
        raise TrainingError('TOKENIZER_PREVIEW_ASSET_UNAVAILABLE') from None


def _preview_row(row, tokenizer, special, eos_id, maximum_tokens):
    prefix, completion = encode_training_row(row, tokenizer, special, eos_id, 'preview')
    rendered = apply_training_template(tokenizer, row.prompt, tokenize=False)
    if not isinstance(rendered, str) or not tokenizer.eos_token:
        raise TrainingError('TRAINING_TOKENIZER_INVALID')
    prompt_tokens = len(prefix)
    total_tokens = prompt_tokens + len(completion)
    excess_index = None
    excess_section = None
    if total_tokens > maximum_tokens:
        excess_index = maximum_tokens
        if excess_index < prompt_tokens:
            excess_section = 'prompt'
        elif excess_index == total_tokens - 1:
            excess_section = 'eos'
        else:
            excess_section = 'completion'
    return SamplePreview(row.line_number, row.content_hash(), rendered, row.completion,
                         tokenizer.eos_token, prompt_tokens, len(completion), total_tokens,
                         excess_index, excess_section)


def preview_samples(model_directory: Path, model_catalog_sha256: str, samples: list[Row] | tuple[Row, ...],
                    maximum_tokens: int) -> TokenizerPreview:
    """Preview authorized canonical samples from a protected installed model path."""
    _validate_samples(samples)
    max_context, assets = _approved_assets(model_catalog_sha256)
    token_limit = CONTRACT['configuration']['properties']['maximum_tokens']
    maximum_allowed = min(token_limit['maximum'], max_context)
    if type(maximum_tokens) is not int or not token_limit['minimum'] <= maximum_tokens <= maximum_allowed:
        raise TrainingError('TOKENIZER_PREVIEW_TOKEN_LIMIT')
    with TemporaryDirectory(prefix='ownsolid-tokenizer-preview-') as directory:
        verified = Path(directory)
        _copy_tokenizer(model_directory, assets, verified)
        _verify_runtime()
        try:
            tokenizer = load_training_tokenizer(verified)
        except (ImportError, OSError, ValueError, RuntimeError):
            raise TrainingError('TOKENIZER_PREVIEW_RUNTIME_UNAVAILABLE') from None
        special, eos_id = training_tokenizer_tokens(tokenizer)
        results = tuple(_preview_row(row, tokenizer, special, eos_id, maximum_tokens) for row in samples)
    return TokenizerPreview(model_catalog_sha256, maximum_tokens, results)
