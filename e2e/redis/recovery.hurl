# The cache works again once Redis is back: the pool replaces its broken connections.

GET {{base_url}}/whoami-cache?back={{run}}
[Options]
variable: run={{newUuid}}
HTTP 200

# Retried until a request is a hit (an earlier one stored the response)
GET {{base_url}}/whoami-cache?back={{run}}
[Options]
retry: 10
retry-interval: 1s
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; hit; ttl="
