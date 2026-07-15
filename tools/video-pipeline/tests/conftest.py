"""Pytest config: make the worker modules (siblings of tests/) importable.

The worker is a flat set of top-level modules (config.py, cache.py, ...) rather
than a package, so we put their directory on sys.path for the tests.
"""

import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))
