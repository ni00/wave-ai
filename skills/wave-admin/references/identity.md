# Database and identity initialization

`wave init-db` targets new databases; historical schema migrations are not promised.
Confirm the target and intended operation from the user's request, rather than using it
to repair arbitrary schema errors. It reads WAVE_DATABASE_URL without requiring model
or storage credentials.

```bash
wave init-db
wave bootstrap -org demo -user admin -key-label laptop
```

bootstrap creates persistent identities/keys; it is not a test of an existing Key. Keys
are displayed once. Default human output includes a label; `-format json` emits an
api_key object, and `-format key` emits only the raw Key. To connect the local CLI directly,
use a shell that supports pipefail:

```bash
set -o pipefail
wavectl config set local --url http://localhost:8080
wave bootstrap -org demo -user admin -key-label laptop -format key \
  | wavectl config set local --key-stdin
wavectl config use local
wavectl doctor
```

Run this pipeline only when Key issuance is requested and the target is established.
Do not print the Key in reports, commit it or treat it as a model-provider credential.
If saving the profile fails, do not assume Key creation also failed; inspect the outcome
before repeating bootstrap. Existing keys can be read through --key-stdin from a private
file specified by the user.

If bootstrap is authorized but changing the default connection is not, save the profile
without running config use. `config unset-key NAME` only removes the local copy; it does
not revoke the server-side Key and must not be reported as revocation.
