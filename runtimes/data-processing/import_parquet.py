"""Decode a private staged Parquet file into one schema line and bounded JSON rows."""

import base64
import decimal
import json
import resource
import sys
import uuid


class ImportLimit(Exception):
    """The file exceeds an intake resource budget."""


def check_footer(source, metadata_bytes):
    if source.read(4) != b"PAR1" or source.seek(0, 2) < 12:
        raise ValueError
    source.seek(-8, 2)
    tail = source.read(8)
    metadata_size = int.from_bytes(tail[:4], "little")
    if tail[4:] != b"PAR1" or not 0 < metadata_size <= source.tell() - 12:
        raise ValueError
    if metadata_size > metadata_bytes:
        raise ImportLimit
    source.seek(0)


def json_value(value):
    if isinstance(value, bytes):
        return base64.b64encode(value).decode("ascii")
    if isinstance(value, (decimal.Decimal, uuid.UUID)):
        return str(value)
    raise TypeError


def scalar_value(scalar, arrow):
    if not scalar.is_valid:
        return None
    if arrow.types.is_temporal(scalar.type) and not arrow.types.is_interval(scalar.type):
        # Python datetime can truncate nanoseconds. Keep the exact count and Arrow unit.
        return scalar.value
    if isinstance(scalar, arrow.StructScalar):
        return {field.name: scalar_value(scalar[index], arrow) for index, field in enumerate(scalar.type)}
    if isinstance(scalar, arrow.MapScalar):
        return [[scalar_value(pair[0], arrow), scalar_value(pair[1], arrow)] for pair in scalar.values]
    if isinstance(scalar, (arrow.ListScalar, arrow.LargeListScalar, arrow.FixedSizeListScalar)):
        return [scalar_value(item, arrow) for item in scalar.values]
    if isinstance(scalar, arrow.DictionaryScalar):
        return scalar_value(scalar.value, arrow)
    return scalar.as_py()


def encode(value):
    return json.dumps(value, ensure_ascii=False, allow_nan=False, separators=(",", ":"), default=json_value).encode("utf-8")


def inspect_type(data_type, arrow):
    if arrow.types.is_struct(data_type):
        names = {field.name for field in data_type}
        if len(names) != data_type.num_fields:
            raise ValueError
    for index in range(data_type.num_fields):
        inspect_type(data_type.field(index).type, arrow)


def emit(parquet, arrow, source, batch_rows, metadata_bytes, group_bytes, row_bytes, fields):
    check_footer(source, metadata_bytes)
    with parquet.ParquetFile(
        source,
        memory_map=False,
        pre_buffer=False,
        thrift_string_size_limit=metadata_bytes,
        thrift_container_size_limit=metadata_bytes,
        page_checksum_verification=True,
        arrow_extensions_enabled=False,
    ) as reader:
        schema = reader.schema_arrow
        if not 0 < len(schema) <= fields:
            raise ImportLimit
        names = schema.names
        if len(set(names)) != len(schema):
            raise ValueError
        for field in schema:
            inspect_type(field.type, arrow)
        header = encode({field.name: str(field.type) for field in schema})
        if len(header) > row_bytes:
            raise ImportLimit
        output = sys.stdout.buffer
        output.write(header + b"\n")
        for group_index in range(reader.num_row_groups):
            group = reader.metadata.row_group(group_index)
            if not 0 <= group.total_byte_size <= group_bytes:
                raise ImportLimit
            if any(group.column(index).file_path for index in range(group.num_columns)):
                raise ValueError
            read_rows = 0
            for batch in reader.iter_batches(batch_size=batch_rows, row_groups=[group_index], use_threads=False):
                columns = batch.columns
                for row_index in range(batch.num_rows):
                    try:
                        row = encode({
                            name: scalar_value(column[row_index], arrow)
                            for name, column in zip(names, columns, strict=True)
                        })
                    except (TypeError, ValueError, OverflowError):
                        row = b"!"
                    output.write((row if len(row) <= row_bytes else b"!") + b"\n")
                read_rows += batch.num_rows
            if read_rows != group.num_rows:
                raise ValueError
        output.flush()


def main(path, batch_rows, metadata_bytes, group_bytes, memory_bytes, row_bytes, fields):
    if sys.platform == "linux":
        resource.setrlimit(resource.RLIMIT_AS, (memory_bytes, memory_bytes))
    try:
        import pyarrow as arrow
        import pyarrow.parquet as parquet
    except ImportError:
        return 2
    try:
        with open(path, "rb") as source:
            emit(parquet, arrow, source, batch_rows, metadata_bytes, group_bytes, row_bytes, fields)
    except MemoryError:
        return 4
    except ImportLimit:
        return 4
    except (OSError, ValueError, TypeError, OverflowError, RecursionError):
        return 3
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1], *(int(value) for value in sys.argv[2:])))
