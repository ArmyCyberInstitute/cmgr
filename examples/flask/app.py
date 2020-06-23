#!/usr/bin/env python3
from flask import Flask, render_template, request, redirect, session, flash, url_for
import sqlite3
import hashlib

app = Flask(__name__)

app.flag = "{{flag}}"

app.challenge_name = "No Escape"
app.secret_key = b"{{secret_key}}"

UNSAFE_QUERY = "SELECT username FROM users WHERE username = '{}' AND pwHash = '{}'"

# Password for admin: 'SuperSecretImpossibleToGuessPassword'
INITIALIZATION_QUERY = """
CREATE TABLE IF NOT EXISTS users (
    username TEXT NOT NULL PRIMARY KEY,
    pwHash TEXT NOT NULL
);

INSERT INTO users (username, pwHash) VALUES
    ("admin", "68933979c6ac2cd3b935311c1d9a7d9dd575ca834fa896ea004791cb45ac2621"),
    ("houdini", "((not a hash))");
"""

@app.before_first_request
def init():
    cur = sqlite3.connect("users.db").cursor()
    try:
        cur.executescript(INITIALIZATION_QUERY)
        cur.commit()
    except Exception as e:
        pass

@app.route("/")
@app.route("/index.html")
def home():
    err = None
    if "error" in session:
        err = session["error"]
        session.pop("error")
    return render_template("index.html",
        challenge_name=app.challenge_name,
        loggedin=("username" in session),
        error=err)

@app.route("/login", methods=['POST'])
def login():
    username = None
    password = None
    pwHash = "INVALID"
    if 'username' in request.form:
        username = request.form['username']
    if 'password' in request.form:
        password = request.form['password']
        pwHash = hashlib.sha256(password.encode()).hexdigest()

    if username:
        cur = sqlite3.connect("users.db").cursor()
        try:
            result = cur.execute(UNSAFE_QUERY.format(
                    username, # This is the vulnerable part
                    pwHash)
                ).fetchone()
            username = result[0] if result else None
        except Exception as e:
            session["error"] = (UNSAFE_QUERY.format(username, pwHash))
            username = None

    if username:
        session["username"] = username

    if username == 'houdini':
        flash("Welcome Houdini, here's your flag: {}".format(app.flag))
    elif username:
        result = cur.execute("SELECT pwHash FROM users WHERE username='houdini'").fetchone()[0]
        flash("Welcome {}!  The \"hash\" for account 'houdini' is '{}'.".format(username, result))
    else:
        flash("Login failed!")

    return redirect(url_for("home"))

@app.route("/logout", methods=['POST'])
def logout():
    if "username" in session:
        session.pop("username")
        flash("Successfully logged out")
    else:
        flash("Not logged in!")
    return redirect(url_for("home"))

if __name__ == '__main__':
    app.run(debug=True, host='0.0.0.0', port=8000)
