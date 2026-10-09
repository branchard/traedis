# Invalidation (RFC 9111 §4.4): a non-error response (2xx, 3xx) to an unsafe request removes the stored
# responses of its URI. whoami answers 200 to any method, placeholder answers 405 to anything but GET and HEAD.

GET {{base_url}}/whoami-cache?run={{run}}
[Options]
variable: run={{newUuid}}
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; fwd=uri-miss; fwd-status=200"

# Another URI: the same path with another query
GET {{base_url}}/whoami-cache?run={{run}}&other=1
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; fwd=uri-miss; fwd-status=200"

GET {{base_url}}/whoami-cache?run={{run}}
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; hit; ttl="

# The POST succeeds: the stored response of its URI is gone before the client gets the answer
POST {{base_url}}/whoami-cache?run={{run}}
HTTP 200
[Asserts]
header "Cache-Status" matches /^traedis; fwd=method; fwd-status=200; detail=invalidated$/

GET {{base_url}}/whoami-cache?run={{run}}
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; fwd=uri-miss; fwd-status=200"

# The other URI was not touched
GET {{base_url}}/whoami-cache?run={{run}}&other=1
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; hit; ttl="

# PUT and DELETE are unsafe too
PUT {{base_url}}/whoami-cache?run={{run}}
HTTP 200
[Asserts]
header "Cache-Status" matches /^traedis; fwd=method; fwd-status=200; detail=invalidated$/

GET {{base_url}}/whoami-cache?run={{run}}
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; fwd=uri-miss; fwd-status=200"

DELETE {{base_url}}/whoami-cache?run={{run}}
HTTP 200
[Asserts]
header "Cache-Status" matches /^traedis; fwd=method; fwd-status=200; detail=invalidated$/

GET {{base_url}}/whoami-cache?run={{run}}
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; fwd=uri-miss; fwd-status=200"

# OPTIONS is a safe method: it invalidates nothing
OPTIONS {{base_url}}/whoami-cache?run={{run}}
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; fwd=method"
header "Cache-Status" not contains "invalidated"

GET {{base_url}}/whoami-cache?run={{run}}
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; hit; ttl="


# An error is not a state change: the stored response is kept

GET {{base_url}}/placeholder-cache/600x600?invalidation={{run}}
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; fwd=uri-miss; fwd-status=200"

POST {{base_url}}/placeholder-cache/600x600?invalidation={{run}}
HTTP 405
[Asserts]
header "Cache-Status" matches /^traedis; fwd=method; fwd-status=405$/

GET {{base_url}}/placeholder-cache/600x600?invalidation={{run}}
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; hit; ttl="


# Every variant of the URI is invalidated

GET {{base_url}}/whoami-vary?invalidation={{run}}
Accept-Language: fr
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; fwd=uri-miss; fwd-status=200"

GET {{base_url}}/whoami-vary?invalidation={{run}}
Accept-Language: en
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; fwd=vary-miss; fwd-status=200"

POST {{base_url}}/whoami-vary?invalidation={{run}}
HTTP 200
[Asserts]
header "Cache-Status" contains "detail=invalidated"

# Not even a marker is left: uri-miss, not vary-miss
GET {{base_url}}/whoami-vary?invalidation={{run}}
Accept-Language: en
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; fwd=uri-miss; fwd-status=200"

GET {{base_url}}/whoami-vary?invalidation={{run}}
Accept-Language: fr
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; fwd=vary-miss; fwd-status=200"
