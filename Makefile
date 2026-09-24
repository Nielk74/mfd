.PHONY: test check up down smoke monitoring
test:
	go test -race ./...
	python3 -m unittest discover -s scripts -p 'test_*.py'
check: test
	go vet ./...
	python3 scripts/build_dashboards.py --check
	docker compose config --quiet
dashboards:
	python3 scripts/build_dashboards.py
up:
	docker compose up -d --build --wait
down:
	docker compose --profile monitoring down
smoke:
	python3 scripts/smoke.py
monitoring:
	docker compose --profile monitoring up -d --wait
