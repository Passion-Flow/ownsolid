import sys
import pyarrow as pa
import pyarrow.parquet as pq

prompt = None if sys.argv[1] == "invalid" else "Who, me?"
pq.write_table(pa.table({
    "source_id": ["first", "second"],
    "prompt": [prompt, "Color"],
    "completion": ["Yes.\nAgain.", "Blue"],
}), sys.stdout.buffer, row_group_size=1, write_page_checksum=True)
