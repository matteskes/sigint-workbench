#!/bin/sh
# CI helper for the e2e-ui workflow job: block until the compose stack
# answers on its published health surfaces — api-gateway /health,
# ws-hub /health (loopback publish, §3.2), and the recorder behind the
# gateway via GET /api/recordings.  Called before the resilience suite
# and again after it (its restart tests shuffle containers).
set -eu
for i in $(seq 1 60); do
  gw=$(curl -s -o /dev/null -w '%{http_code}' http://localhost:8080/health || true)
  hub=$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8081/health || true)
  rec=$(curl -s -o /dev/null -w '%{http_code}' http://localhost:8080/api/recordings || true)
  echo "try $i: gateway=$gw hub=$hub recorder=$rec"
  if [ "$gw" = 200 ] && [ "$hub" = 200 ] && [ "$rec" = 200 ]; then
    exit 0
  fi
  sleep 2
done
echo "FAIL: stack not healthy after 120s"
docker compose ps
docker compose logs --tail 40
exit 1
