"""Required frozen training parameters; defaults belong to the canonical release contract."""
from dataclasses import asdict, dataclass
from decimal import Decimal, InvalidOperation
import hashlib
import json
import re
from pathlib import Path
import platform
import stat
from . import VERSION
from .configuration_generated import CONTRACT
from .data import TrainingError


def runtime_contract():
    path = Path(__file__).resolve().parents[1] / 'runtime.json'
    info = path.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_mode & 0o022:
        raise TrainingError('TRAINING_EXECUTION_IDENTITY_REQUIRED')
    value = json.loads(path.read_text())
    if value['version'] != VERSION or value['python'] != platform.python_version():
        raise TrainingError('TRAINING_EXECUTION_IDENTITY_REQUIRED')
    return value


@dataclass(frozen=True)
class Configuration:
    device: str
    maximum_steps: int
    maximum_tokens: int
    batch_size: int
    gradient_accumulation_steps: int
    learning_rate: str
    rank: int
    alpha: int
    dropout: str
    checkpoint_steps: int
    seed: int
    threads: int

    def __post_init__(self):
        values = asdict(self)
        fields = CONTRACT['configuration']['properties']
        if set(values) != set(fields):
            raise TrainingError('TRAINING_CONFIGURATION_INVALID')
        for key, value in values.items():
            field = fields[key]
            if type(value) is not (int if field['type'] == 'integer' else str):
                raise TrainingError('TRAINING_CONFIGURATION_INVALID')
            if 'enum' in field and value not in field['enum']:
                raise TrainingError('TRAINING_CONFIGURATION_INVALID')
            if 'minimum' in field and not field['minimum'] <= value <= field['maximum']:
                raise TrainingError('TRAINING_CONFIGURATION_INVALID')
            if 'pattern' in field and not re.fullmatch(field['pattern'], value):
                raise TrainingError('TRAINING_CONFIGURATION_INVALID')
            if 'decimal_minimum' in field:
                try:
                    if not Decimal(field['decimal_minimum']) <= Decimal(value) <= Decimal(field['decimal_maximum']):
                        raise TrainingError('TRAINING_CONFIGURATION_INVALID')
                except InvalidOperation:
                    raise TrainingError('TRAINING_CONFIGURATION_INVALID') from None
        if self.maximum_steps % self.checkpoint_steps or self.maximum_steps // self.checkpoint_steps > CONTRACT['recipe']['maximum_checkpoints']:
            raise TrainingError('TRAINING_CONFIGURATION_INVALID')

    def digest(self):
        return hashlib.sha256(json.dumps(asdict(self), sort_keys=True, separators=(',', ':')).encode()).hexdigest()
