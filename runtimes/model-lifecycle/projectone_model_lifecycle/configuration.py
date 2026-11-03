"""Required bounded preparation settings from the versioned shared recipe."""
from dataclasses import dataclass, asdict
import hashlib
import json
from pathlib import Path

REPO = Path(__file__).resolve().parents[3]
CONTRACT_FILE = REPO / 'contracts/model-lifecycle/v1/recipe.json'
CONTRACT = json.loads(CONTRACT_FILE.read_text())
LABELS = tuple(CONTRACT['labels'])


class PreparationError(Exception):
    def __init__(self, code, line_number=None):
        self.code = code
        self.line_number = line_number
        super().__init__(code)


def compact(value):
    return json.dumps(value, sort_keys=True, separators=(',', ':'), ensure_ascii=False, allow_nan=False).encode('utf-8')


def digest(value):
    return hashlib.sha256(compact(value)).hexdigest()


@dataclass(frozen=True)
class Configuration:
    device: str
    maximum_input_tokens: int
    maximum_new_tokens: int
    seed: int
    threads: int
    minimum_accuracy_basis_points: int
    maximum_regression_basis_points: int

    def __post_init__(self):
        for key, value in asdict(self).items():
            field = CONTRACT['configuration']['properties'][key]
            if type(value) is not (str if field['type'] == 'string' else int):
                raise PreparationError('MODEL_PREPARATION_CONFIGURATION_INVALID')
            if 'enum' in field and value not in field['enum']:
                raise PreparationError('MODEL_PREPARATION_CONFIGURATION_INVALID')
            if 'minimum' in field and not field['minimum'] <= value <= field['maximum']:
                raise PreparationError('MODEL_PREPARATION_CONFIGURATION_INVALID')
        if self.maximum_input_tokens + self.maximum_new_tokens > CONTRACT['maximum_context_tokens']:
            raise PreparationError('MODEL_PREPARATION_CONFIGURATION_INVALID')

    def digest(self):
        return digest(asdict(self))


def prompt_fingerprint(prompt):
    if not isinstance(prompt, str):
        raise PreparationError('MODEL_PREPARATION_INPUT_INVALID')
    try:
        value = prompt.replace('\r\n', '\n').replace('\r', '\n').strip(' \t\n').encode('utf-8', errors='strict')
    except UnicodeError:
        raise PreparationError('MODEL_PREPARATION_INPUT_INVALID') from None
    return hashlib.sha256(value).hexdigest()


def instruction_prompt(prompt):
    return CONTRACT['instruction'] + CONTRACT['instruction_separator'] + prompt
