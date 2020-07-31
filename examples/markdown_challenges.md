# Markdown Challenge Specification

- Namespace: cmgr/examples
- Type: flask
- Category: example
- Points: 1
- Templatable: yes
- MaxUsers: 0

## Description

This is a static description of the challenge that is intended to be shown to
the user and will be the same across all instances of the challenge.

## Details

This is templated information for the challenge that can use additional
build-specific information to present information.  In particular, the following
templates are allowed (anything else is invalid):
- `{{url_for("file", "display text")}}`
- `{{http_base("port_name")}}` (URL prefix for HTTP requests to the named port)
- `{{port("port_name")}}` (The specific port number competitors will see which
may not be the same number as exposed by Docker if the front-end is proxying
connections.)
- `{{server("port_name")}}` (hostname which hosts for connecting to the
associated port for the challenge)
- `{{lookup("key")}}` ("key" must have been published in `metadata.json` when creating a build)
- `{{link("port_name", "/url/in/challenge")}}` (convenience wrapper for generating an HTML link)
- `{{link_as("port_name", "/url/in/challenge", "display text")}}` (convenience
wrapper for generating an HTML link with text different from the URL)

**Note:** As a convenience, `port_name` can be omitted any time the challenge only
publishes a single port to competitors.  For templates that only take
`port_name` as an argument, the parentheses should be omitted when using this
convenience.  The template strings exposed to front-ends will always be
normalized to include `port_name` and have replaced `link` and `linkAs` with
an HTML href tag that uses `http_base`.

## Hints

- A list of hints for the end user.
- The hints are all templatable.
- Whether there is a cost for displaying them is up to the front-end system

## Tags

- example
- markdown

## Attributes

- Organization: ACI
- Created: 2020-06-24

## Extra Sections

Any `h2` sections (i.e. lines starting with `##`) that don't match one of the
headers above (not including "Extra Sections") will be parsed and added as
additional attributes where the header text is the key and the value is raw
text (i.e. no Markdown conversions) up to but not including the next `h2`
header.  Whitespace at the start and end of this block of text is stripped,
but all other whitespace is preserved exactly as written.

## Mandatory Sections

There are only a few mandatory parts of this structure: the title line which
is interpreted as the challenge name, the "type" entry (must be a list bullet
in the block immediately following the title), and at least one templated
reference to each artifact file and port exposed to the competitor (most
likely in the "details" section).  Although not required, the "namespace"
entry is highly encouraged as it minimizes the likelihood of naming conflicts
if challenges are released and/or merged with other sources.
