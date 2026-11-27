"""Statement coverage of the files behind the REST endpoints of Sprint 2 (from a Go coverage profile)."""
import sys
from collections import defaultdict

SCOPE = [
    "internal/core/service/scope.go",
    "internal/core/service/academic_service.go",
    "internal/core/service/people_service.go",
    "internal/core/service/attendance_service.go",
    "internal/core/service/assessment_service.go",
    "internal/core/service/auth_service.go",
    "internal/adapter/inbound/rest/rest.go",
    "internal/adapter/inbound/grpc/academic_server.go",
    "internal/adapter/inbound/grpc/people_server.go",
    "internal/adapter/inbound/grpc/attendance_server.go",
    "internal/adapter/inbound/grpc/assessment_server.go",
    "internal/adapter/inbound/grpc/errors.go",
    "internal/adapter/inbound/grpc/client.go",
    "internal/adapter/outbound/postgres/academic_repository.go",
    "internal/adapter/outbound/postgres/people_repository.go",
    "internal/adapter/outbound/postgres/attendance_repository.go",
    "internal/adapter/outbound/postgres/assessment_repository.go",
]

total, covered = defaultdict(int), defaultdict(int)
with open(sys.argv[1]) as f:
    next(f)
    blocks = {}
    for line in f:
        loc, stmts, count = line.rsplit(" ", 2)
        # a block can appear once per run; it counts as covered if any run hit it
        prev = blocks.get(loc, (int(stmts), 0))
        blocks[loc] = (int(stmts), max(prev[1], int(count)))
for loc, (stmts, count) in blocks.items():
    name = loc.split(":")[0].removeprefix("api/")
    total[name] += stmts
    if count > 0:
        covered[name] += stmts

all_t = all_c = 0
print(f"{'archivo':<62} {'cobertura':>9}")
for name in SCOPE:
    t, c = total.get(name, 0), covered.get(name, 0)
    all_t, all_c = all_t + t, all_c + c
    print(f"{name:<62} {100 * c / t if t else 0:8.1f}%")
print(f"{'TOTAL módulos del alcance':<62} {100 * all_c / all_t if all_t else 0:8.1f}%")
