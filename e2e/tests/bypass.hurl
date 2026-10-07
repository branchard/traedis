# Requests the cache never handles: the response writer is not wrapped (Yaegi would
# hide http.Flusher from the backend), nothing is looked up nor stored.

# Stored first: none of the requests below may be answered from it
GET {{base_url}}/whoami-cache?run={{run}}
[Options]
variable: run={{newUuid}}
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; fwd=uri-miss; fwd-status=200"

# Methods other than GET and HEAD
POST {{base_url}}/whoami-cache?run={{run}}
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; fwd=method"

# Event streams
GET {{base_url}}/whoami-cache?run={{run}}
Accept: text/event-stream
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; fwd=bypass"

# Upgrade requests (WebSockets)
GET {{base_url}}/whoami-cache?run={{run}}
Connection: Upgrade
Upgrade: websocket
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; fwd=bypass"
