#!/usr/bin/env python3

import re
import os
import uuid
import json
import random
import subprocess
import tempfile

from flask_session import Session
from flask import Flask, render_template, session, request, abort, redirect, url_for, flash

ADMIN_KEY = '{{seed}}'

NODE_PATH = '/usr/bin/node'
NODE_PATH = '/usr/local/bin/node'


app = Flask(__name__)
app.config['SECRET_KEY'] = '{{seed}}'
app.secret_key = '{{seed}}'
app.config['SESSION_TYPE'] = 'filesystem'
app.config['SESSION_COOKIE_HTTPONLY'] = False
app.config['TEMPLATES_AUTO_RELOAD'] = True
Session(app)

@app.route('/')
def index():
    return redirect(url_for('cookie'))

# Cookie index
@app.route('/cookie')
def cookie():
    return render_template('new_cookie.html')

# Display a given cookie
@app.route('/cookie/<string:uid>')
def get_cookie(uid):
    cookies = session.get('cookies',{})
    if uid not in cookies:
        return abort(404)
    cookie = cookies[uid]
    if cookie.get('flag',False) and not session.get('admin',False):
        return abort(403)
    return render_template('cookie.html', cookie=cookies[uid], uid=uid)

# View cookies to review
@app.route('/admin')
def admin():
    if not session.get('admin',False):
        return abort(403)
    cookies = session.get('cookies',{})
    cookies = [(x,y) for x,y in cookies.items() if y['submitted'] and not y['rejected']]
    return render_template('admin.html', cookies=cookies)

# Request a cookie to be approved
@app.route('/approve', methods=['POST'])
def approve():

    uid = request.form['cookie']
    cookies = session.get('cookies',{})
    if uid not in cookies:
        return abort(400)

    sid = get_session_id()
    if sid in procs:
        return redirect(url_for('get_cookie',uid=uid))

    session['cookies'][uid]['submitted'] = True
    start_admin(uid)
    return redirect(url_for('get_cookie',uid=uid))

# Create a new cookie
@app.route('/cookie/new', methods=['POST'])
def new_cookie():
    ingredients = request.form.getlist('ingredients')

    if not 'cookies' in session:
        # Store the flag into the session
        session['cookies'] = {str(uuid.uuid4()):{
            'name':'Flag Cookie',
            'recipe':'<p>1. Preheat the oven to 350</p><p>2. Add one {{flag}}</p><p>3. Bake for 1337 minutes and allow to cool</p>',
            'flag': True,
            'submitted':True,'rejected':False
        }}

    # Build XSS location
    recipe = '<p>1. Preheat the oven to 350</p>'
    count = 1
    for i,ing in enumerate(ingredients):
        if ing == 'Add ingredient':
            continue
        count+=1
        recipe += f'''
<p>{count}. {random.choice(['Mix together','Add in','Stir in'])} {random.randint(1,5)} {random.choice(['cups','teaspoons','tablespoons','ounces'])} of {ing}</p>'''

    recipe += f'\n<p>{count+1}. Bake for {random.randint(30,60)} minutes and allow to cool</p>'

    uid = str(uuid.uuid4())
    session['cookies'][uid] = {'name':request.form['name'],'recipe':recipe, 'submitted':False, 'rejected':False}
    return redirect(url_for('get_cookie', uid=uid))

procs = {}

# Clean up any child procs and update the session
@app.before_request
def before_request():
    sid = get_session_id()
    if sid is None or sid not in procs:
        return

    p,uid = procs[sid]

    if p.poll() is None:
        return

    del procs[sid]
    session['cookies'][uid]['rejected'] = True

def start_admin(uid):
    tmpf = save_session_to_file()

    url = f'http://localhost:5000/admin/landing?key={ADMIN_KEY}&session={tmpf}&uid={uid}'

    os.environ['NODE_PATH'] = '/usr/lib/node_modules'
    p = subprocess.Popen(['timeout','10s',NODE_PATH,'./chrome.js',url])
    procs[get_session_id()] = (p,uid)

@app.route('/admin/landing')
def admin_landing():
    if not 'key' in request.args or request.args['key'] != ADMIN_KEY:
        abort(404)
    uid = request.args['uid']
    load_session_from_file(request.args['session'])
    return redirect(url_for('get_cookie', uid=uid))

class FakeRequest():
    def __init__(self, session):
        self.cookies = { app.session_cookie_name: session }

class FakeResponse():
    def set_cookie(*args, **kwargs):
        pass

def get_session(sid):
    return app.session_interface.open_session(app, FakeRequest(sid))

def save_session(ses):
    app.session_interface.save_session(app, ses, FakeResponse())

def get_session_id():
    return request.cookies.get(app.session_cookie_name,None)

def save_session_to_file():
    sid = get_session_id()
    ses = get_session(sid)
    ses['user_session'] = sid
    fd, tmpf = tempfile.mkstemp()
    os.close(fd)
    with open(tmpf,'w') as f:
        json.dump(ses, f)

    return tmpf

def load_session_from_file(tmpf):
    with open(tmpf,'r') as f:
        ses = json.load(f)
    os.unlink(tmpf)
    ses['admin'] = True
    session.update(ses)

if __name__ == '__main__':
    app.run(debug=True, host='0.0.0.0', port=5001)

