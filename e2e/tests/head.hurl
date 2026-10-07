# HEAD (RFC 9111 §4): served from the stored GET, never stored itself.

# A HEAD response is not stored
HEAD {{base_url}}/placeholder-cache/600x600?run={{run}}
[Options]
variable: run={{newUuid}}
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; fwd=uri-miss; fwd-status=200"

GET {{base_url}}/placeholder-cache/600x600?run={{run}}
HTTP 200
[Captures]
body_size: bytes count
[Asserts]
header "Cache-Status" contains "traedis; fwd=uri-miss; fwd-status=200"

# HEAD is served from the stored GET: its headers, no body
HEAD {{base_url}}/placeholder-cache/600x600?run={{run}}
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; hit; ttl="
header "Content-Length" toInt == {{body_size}}
bytes count == 0
