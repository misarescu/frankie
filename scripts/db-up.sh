#!/usr/bin/env bash
# Starts (or reuses) the local dev/test Postgres on localhost:55432.
set -euo pipefail

name=frankie-dev-postgres
if [ "$(docker ps -aq -f "name=^${name}$")" = "" ]; then
	docker run -d --name "$name" -p 55432:5432 \
		-e POSTGRES_PASSWORD=frankie postgres:17 >/dev/null
elif [ "$(docker ps -q -f "name=^${name}$")" = "" ]; then
	docker start "$name" >/dev/null
fi

for _ in $(seq 1 30); do
	if docker exec "$name" pg_isready -U postgres -q; then
		echo "db-up: $name ready on localhost:55432"
		exit 0
	fi
	sleep 1
done
echo "db-up: $name did not become ready" >&2
exit 1
