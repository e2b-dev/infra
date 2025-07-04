The python SDK had a bug: new fields introduced to the API would throw exceptions when processed. In order to work around this, we convert all responses to a list of structs that were valid when the python SDK still had the bug.

We copy the proto files to this folder in order to keep track of what was available when this SDK was supported. These should never be modified.

This entire package can be removed once we no longer have clients that send a user agent of `connect-python`.

