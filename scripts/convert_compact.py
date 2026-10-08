#!/usr/bin/env python3
"""Convert ONLY proper markdown tables to compact style, skip cells with embedded pipes."""
import sys

path = sys.argv[1] if len(sys.argv) > 1 else "docs/test/E2E-TEST-SUITE.md"

with open(path, "r") as f:
    lines = f.readlines()

out = []
i = 0
while i < len(lines):
    line = lines[i].rstrip("\n")
    s = line.strip()
    
    # Check if this looks like a header row of a table: starts with |
    # and has a separator row following (|---|)
    if s.startswith("|"):
        pipe_count = line.count("|")
        
        # Check if next non-empty line is a separator: |---| or |---|---|
        j = i + 1
        while j < len(lines) and lines[j].strip() == "":
            j += 1
        
        if j < len(lines):
            sep_line = lines[j].strip()
            sep_stripped = sep_line.replace(" ", "").replace(":", "")
            if sep_stripped and all(c == "-" or c == "|" for c in sep_stripped):
                # This is a table! Convert to compact style.
                sep_pipes = sep_line.count("|")
                expected_cols = sep_pipes - 1  # columns = pipes - 1
                
                # Convert this header row and all subsequent body rows of this table
                converted_lines = []
                k = i
                while k <= j and k < len(lines):
                    kline = lines[k].rstrip("\n")
                    k_pipe_count = kline.count("|")
                    if k_pipe_count >= 2:
                        parts = kline.split("|")
                        compact_parts = [p.strip() for p in parts]
                        converted_lines.append("|".join(compact_parts) + "\n")
                    else:
                        converted_lines.append(kline + "\n")
                    k += 1
                
                # Now convert body rows until we hit a non-table line
                k = j + 1
                while k < len(lines):
                    bline = lines[k].rstrip("\n")
                    bstripped = bline.strip()
                    
                    # Empty line or non-table line: stop
                    if bstripped == "" or not bstripped.startswith("|"):
                        break
                    
                    b_pipe_count = bline.count("|")
                    # If pipe count matches expected (columns + 1), it's a valid table row
                    if b_pipe_count == expected_cols + 1:
                        parts = bline.split("|")
                        compact_parts = [p.strip() for p in parts]
                        converted_lines.append("|".join(compact_parts) + "\n")
                    else:
                        # Pipe count doesn't match: embedded pipes in cell content.
                        # Flush converted table, leave this line as-is.
                        out.extend(converted_lines)
                        out.append(bline + "\n")
                        i = k + 1
                        break
                    k += 1
                else:
                    # Normal exit of while (no break)
                    pass
                
                out.extend(converted_lines)
                if k <= j:
                    i = j  # was a header/separator only
                else:
                    i = k
                continue
    
    out.append(line + "\n")
    i += 1

with open(path, "w") as f:
    f.writelines(out)

print(f"Conversion complete. Lines processed: {len(lines)}")
