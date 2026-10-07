# Nothing personal is shared. whoami echoes the request headers in its body.

# Authorization without explicit freshness: defaultTtl is not applied, not stored
GET {{base_url}}/whoami-cache?auth={{run}}
Authorization: Bearer e2e-secret
[Options]
variable: run={{newUuid}}
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; fwd=uri-miss; fwd-status=200"
body contains "e2e-secret"

GET {{base_url}}/whoami-cache?auth={{run}}
Authorization: Bearer e2e-secret
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; fwd=uri-miss; fwd-status=200"

# Another client does not get the personal response
GET {{base_url}}/whoami-cache?auth={{run}}
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; fwd=uri-miss; fwd-status=200"
body not contains "e2e-secret"

# Cookie without explicit freshness: defaultTtl is not applied, not stored
GET {{base_url}}/whoami-cache?cookie={{run}}
Cookie: sid=e2e-secret
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; fwd=uri-miss; fwd-status=200"
body contains "e2e-secret"

GET {{base_url}}/whoami-cache?cookie={{run}}
Cookie: sid=e2e-secret
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; fwd=uri-miss; fwd-status=200"

GET {{base_url}}/whoami-cache?cookie={{run}}
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; fwd=uri-miss; fwd-status=200"
body not contains "e2e-secret"

# Authorization with `public` (RFC 9111 §3.5): placeholder allows sharing its response
GET {{base_url}}/placeholder-cache/600x600?auth={{run}}
Authorization: Bearer e2e-secret
HTTP 200
[Asserts]
header "Cache-Control" contains "public"
header "Cache-Status" contains "traedis; fwd=uri-miss; fwd-status=200"

GET {{base_url}}/placeholder-cache/600x600?auth={{run}}
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; hit; ttl="
