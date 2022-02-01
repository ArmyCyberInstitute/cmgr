# Cookie Monster

- namespace: cy450/lsn10
- type: node
- category: Web Security
- points: 200

## Description

Lets make some yummy cookies! Maybe you can even find some extra tasty ones.

## Details

{{link_as("/", "Web Portal")}}

## Hints

- The admin can see the flag on the admin page.

- You can control the value so `select` all you want.

- If you need an endpoint for a callback, [requestbin.io](https://requestbin.io)
  is a useful resource.  You could also run a simple server on your tools VM
  using a command like `python -m SimpleHTTPServer`

## Example Overview

This challenge shows how to extend the `node` challenge type for usecases
where the underlying server may not use Node, but that Node is used for other
functionality (such as [Puppeteer](https://puppeteer.github.io/puppeteer/)).
In this example, the `preinstall` and `start` scripts for _npm_ are overridden
in `package.json` to install and run a Flask server which then uses Puppeteer
to simulate an admin browsing an XSS-vulnerable site.

## Solution

1. The player creates a recipe and finds that the name field does not contain
an XSS vulnerability.

2. The player modifies the ingredient list to have a value of an XSS payload.
This will be saved in the recipe and can trigger the XSS on page load.

3. The player can craft the XSS payload to retrieve the session cookie and
exfil it to a controlled place (such as postbin)

4. The player submits the malicious recipe to the admin, and through the XSS,
retrieves the admin's session cookie.

5. The player uses the stolen session cookie to access the admin's account and
view the `/admin` endpoint to retrieve the flag

## Learning Objective

By the end of this challenge, competitors will have gained experience finding
and exploiting an XSS vulnerability to steal cookies and dump the contents of a
database.

## Attributes

- time: 60-120 minutes
- difficulty: Medium
- organization: GRIMM
- event: aacs4
