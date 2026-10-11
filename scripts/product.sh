# Sourced by build scripts: exports product_* variables from product.json,
# the single source of the product identity.
eval "$(python3 - "$(dirname "${BASH_SOURCE[0]}")/../product.json" <<'PY'
import json, shlex, sys
p = json.load(open(sys.argv[1], encoding='utf-8'))
for key in ('name', 'identifier', 'configDir', 'legacyStorageDir', 'devStorageDir', 'linuxBinary'):
    print(f'product_{key}={shlex.quote(p[key])}')
PY
)"
