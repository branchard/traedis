# Routes without the middleware: the cache is not involved.

GET {{base_url}}/whoami?run={{run}}
[Options]
variable: run={{newUuid}}
HTTP 200
[Asserts]
header "Cache-Status" not exists

GET {{base_url}}/whoami?run={{run}}
HTTP 200
[Asserts]
header "Cache-Status" not exists
header "Age" not exists

GET {{base_url}}/placeholder/600x600?run={{run}}
HTTP 200
[Asserts]
header "Cache-Status" not exists
