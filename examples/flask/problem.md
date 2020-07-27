# Flask (Sqlite)

- namespace: cmgr/examples
- type: flask
- category: Web Security
- points: 50

## Description

Some magician we've never heard of is trying to sell us on the idea of a
"digital" magic show where the magician logs in using an impossible
password.  For added assurances, one lucky audience member will be given
credentials to login and see the hash of the password as proof the password is
impossible.  We're willing to bet the secret to this magic trick is not all
that complicated.

## Details

{{link_as("/", "Web Portal")}}

## Hints

- Inexperienced web application developers don't always esacpe/sanitize user inputs in there database query strings.  This frequently allows [SQL injection](https://en.wikipedia.org/wiki/SQL_injection) attacks that result in unintended behavior.

- The developer was pretty new, so just causing the query to error out may get you more information for the exploit.  What happens when you use a single `'` or `"` in each of the login fields?

- You'll need to login as a specific user.  If you're new to SQL syntax, [this](https://www.w3schools.com/sql/sql_where.asp) might be useful resource for understanding the intended query and how you can manipulate it for your purposes.

## Tags

- SQL
- sqlite
- flask
- example

## Attributes

- organization: ACI
- version: 1.0.1
- event: aacs4
