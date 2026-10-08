# Vary (RFC 9111 §4.1): one stored variant per value of the request headers the response varies by.
# /whoami-vary answers with `Vary: Accept-Language, Accept-Encoding` and `Cache-Control: max-age=60`; whoami
# echoes the request headers in its body, and Traefik compresses it (br, then gzip) for the requests that accept it.
# Its cache is configured with `maxVariants: 3`.

# First response of the URI
GET {{base_url}}/whoami-vary?lang={{run}}
Accept-Language: fr
[Options]
variable: run={{newUuid}}
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; fwd=uri-miss; fwd-status=200"
header "Vary" == "Accept-Language, Accept-Encoding"
body contains "Accept-Language: fr"

GET {{base_url}}/whoami-vary?lang={{run}}
Accept-Language: fr
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; hit; ttl="
# The Vary of the backend is sent as it was
header "Vary" == "Accept-Language, Accept-Encoding"
body contains "Accept-Language: fr"

# Another value is another variant of a known URI
GET {{base_url}}/whoami-vary?lang={{run}}
Accept-Language: en
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; fwd=vary-miss; fwd-status=200"
body contains "Accept-Language: en"

GET {{base_url}}/whoami-vary?lang={{run}}
Accept-Language: en
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; hit; ttl="
body contains "Accept-Language: en"

# A request without the header has its own variant
GET {{base_url}}/whoami-vary?lang={{run}}
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; fwd=vary-miss; fwd-status=200"
body not contains "Accept-Language"

GET {{base_url}}/whoami-vary?lang={{run}}
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; hit; ttl="
body not contains "Accept-Language"

# The first variant was not replaced; HEAD is served from it
HEAD {{base_url}}/whoami-vary?lang={{run}}
Accept-Language: fr
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; hit; ttl="

# Request headers that are not listed by Vary don't make variants
GET {{base_url}}/whoami-vary?lang={{run}}
Accept-Language: fr
X-Device: tv
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; hit; ttl="
body contains "Accept-Language: fr"
body not contains "X-Device"

# maxVariants (3: fr, en and no header): another variant is not stored
GET {{base_url}}/whoami-vary?lang={{run}}
Accept-Language: de
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; fwd=vary-miss; fwd-status=200"
body contains "Accept-Language: de"

GET {{base_url}}/whoami-vary?lang={{run}}
Accept-Language: de
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; fwd=vary-miss; fwd-status=200"

# and it evicted none of the stored ones
GET {{base_url}}/whoami-vary?lang={{run}}
Accept-Language: en
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; hit; ttl="
body contains "Accept-Language: en"


# Accept-Encoding: variants are stored by Content-Encoding and shared by all the requests accepting it.

GET {{base_url}}/whoami-vary?coding={{run}}
Accept-Encoding: gzip, deflate, br, zstd
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; fwd=uri-miss; fwd-status=200"
header "Content-Encoding" == "br"
# The request reaches the backend as the client sent it
body contains "Accept-Encoding: gzip, deflate, br, zstd"

# Another Accept-Encoding accepting br: the same variant (the body is the one of the first request)
GET {{base_url}}/whoami-vary?coding={{run}}
Accept-Encoding: br
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; hit; ttl="
header "Content-Encoding" == "br"
body contains "Accept-Encoding: gzip, deflate, br, zstd"

# A request that does not accept br never gets it: the backend answers
GET {{base_url}}/whoami-vary?coding={{run}}
Accept-Encoding: gzip
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; fwd=vary-miss; fwd-status=200"
header "Content-Encoding" == "gzip"
body contains "Accept-Encoding: gzip"

GET {{base_url}}/whoami-vary?coding={{run}}
Accept-Encoding: gzip;q=1.0, br;q=0
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; hit; ttl="
header "Content-Encoding" == "gzip"

# Without Accept-Encoding: never a compressed variant
GET {{base_url}}/whoami-vary?coding={{run}}
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; fwd=vary-miss; fwd-status=200"
header "Content-Encoding" not exists

GET {{base_url}}/whoami-vary?coding={{run}}
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; hit; ttl="
header "Content-Encoding" not exists

# The three variants are stored; a request accepting several gets br
GET {{base_url}}/whoami-vary?coding={{run}}
Accept-Encoding: gzip, br
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; hit; ttl="
header "Content-Encoding" == "br"


# An uncompressed response does not decide for the requests accepting more codings than its own.

GET {{base_url}}/whoami-vary?identity={{run}}
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; fwd=uri-miss; fwd-status=200"
header "Content-Encoding" not exists

# This request lets the backend compress: it is asked
GET {{base_url}}/whoami-vary?identity={{run}}
Accept-Encoding: gzip
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; fwd=vary-miss; fwd-status=200"
header "Content-Encoding" == "gzip"

GET {{base_url}}/whoami-vary?identity={{run}}
Accept-Encoding: gzip
HTTP 200
[Asserts]
header "Cache-Status" contains "traedis; hit; ttl="
header "Content-Encoding" == "gzip"
