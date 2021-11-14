# Aptitude and Privileges

- Namespace: cmgr/examples
- Type: custom
- Category: Privilege Escalation
- Points: 25
- Templatable: yes
- MaxUsers: 1

## Description

Every developer has been there - you go to build something and the package
dependency you need just isn't installed.  Thankfully for us, our employer has
helpfully given us `sudo` access to install them.  Unfortunately for Alice,
that gives us more privileges than it should.  Can you manage to log into her
home computer?

## Details

The work computer is running SSH.  We've managed to "convince" Eve Smythe to let
us use her credentials:
```
Hostname: {{server}}
Port:     {{port}}
Username: {{lookup("username")}}
Password: {{lookup("password")}}
```

## Hints

- It is harder than you might think to "safely" give package management rights
to end users.

- Alice uses Ubuntu for her home computer and frequently uses SSH to connect
to it from work.

## Learning Objective

By the end of this challenge, competitors should have a basic understanding of
how to use package managers for privilege escalation and where to look for easy
pivot points in a Linux environment.

## Challenge Options

```yaml
init: true
cpus: 0.25
pidslimit: 50

overrides:
    work:
        init: true
        cpus: 0.5
        pidslimit: 50
```

## Tags

- privesc
- example

## Attributes

- author: John Rollinson
- organization: Army Cyber Institute
