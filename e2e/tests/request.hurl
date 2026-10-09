# Request directives (RFC 9111 §5.2.1, §5.4).

# no-store (§5.2.1.5): the response is not stored
GET {{base_url}}/whoami-cache?run={{run}}
Cache-Control: no-store
[Options]
variable: run={{newUuid}}
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; fwd=uri-miss; fwd-status=200"

# only-if-cached (§5.2.1.7): 504 when nothing is stored
GET {{base_url}}/whoami-cache?run={{run}}
Cache-Control: only-if-cached
HTTP 504
[Asserts]
header "Cache-Status" contains "traedis; fwd=uri-miss"

GET {{base_url}}/whoami-cache?run={{run}}
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; fwd=uri-miss; fwd-status=200"

# only-if-cached (§5.2.1.7): served when stored
GET {{base_url}}/whoami-cache?run={{run}}
Cache-Control: only-if-cached
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; hit; ttl="

# no-cache (§5.2.1.4): the stored response is not used
GET {{base_url}}/whoami-cache?run={{run}}
Cache-Control: no-cache
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; fwd=request; fwd-status=200"

# Pragma: no-cache (§5.4), without Cache-Control: same as no-cache
GET {{base_url}}/whoami-cache?run={{run}}
Pragma: no-cache
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; fwd=request; fwd-status=200"

# max-age=0 (§5.2.1.1): the stored response is fresh, but too old for this client (RFC 9211 §2.2: fwd=request)
GET {{base_url}}/whoami-cache?run={{run}}
Cache-Control: max-age=0
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; fwd=request; fwd-status=200"

# min-fresh (§5.2.1.3) beyond defaultTtl (5m): not fresh for long enough
GET {{base_url}}/whoami-cache?run={{run}}
Cache-Control: min-fresh=600
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; fwd=request; fwd-status=200"

# Without directives, the stored response is still served
GET {{base_url}}/whoami-cache?run={{run}}
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; hit; ttl="
