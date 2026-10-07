# Explicit freshness (RFC 9111 §4.2.1): placeholder sends `Cache-Control: public, max-age`.

# First request is a miss
GET {{base_url}}/placeholder-cache/600x600?run={{run}}
[Options]
variable: run={{newUuid}}
HTTP 200
[Captures]
content_type: header "Content-Type"
cache_control: header "Cache-Control"
body_hash: sha256
[Asserts]
header "Cache-Status" contains "traedis; fwd=uri-miss; fwd-status=200"
header "Age" not exists

# Second request is a hit: same response, with its age (§4.2.3) and remaining freshness
GET {{base_url}}/placeholder-cache/600x600?run={{run}}
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; hit; ttl="
header "Age" matches /^\d+$/
header "Content-Type" == {{content_type}}
sha256 == {{body_hash}}
# The Cache-Control sent downstream is never rewritten
header "Cache-Control" == {{cache_control}}
