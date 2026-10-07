# Fail open: with Redis down, the cache is bypassed and the backends still answer.

GET {{base_url}}/placeholder-cache/600x600?down={{run}}
[Options]
variable: run={{newUuid}}
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; fwd=bypass; fwd-status=200; detail=redis"
header "Content-Type" == "image/png"

# Nothing was stored: still bypassed
GET {{base_url}}/placeholder-cache/600x600?down={{run}}
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; fwd=bypass; fwd-status=200; detail=redis"

GET {{base_url}}/whoami-cache?down={{run}}
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; fwd=bypass; fwd-status=200; detail=redis"

HEAD {{base_url}}/whoami-cache?down={{run}}
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; fwd=bypass; fwd-status=200; detail=redis"

# Requests the cache never handles do not need Redis
POST {{base_url}}/whoami-cache?down={{run}}
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; fwd=method"
