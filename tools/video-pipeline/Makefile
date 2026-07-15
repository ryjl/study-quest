.PHONY: install run test clean

# install: create the venv + install deps (including nvidia-*-cu12 CUDA libs)
install:
	uv sync --extra dev

# run: launch the worker via run.sh (sets LD_LIBRARY_PATH for cublas BEFORE
# python starts — must not use `python worker.py` directly, see run.sh)
run:
	./run.sh

test:
	uv run pytest -q

clean:
	rm -rf .venv __pycache__ .pytest_cache
