#!/bin/sh
set -eu
exec python3 -m unittest discover -s tests -p 'test_*.py' -v
