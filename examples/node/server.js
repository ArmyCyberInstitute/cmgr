const express = require('express')
const app = express()
const port = process.env.PORT

app.get('/', (req, res) => res.send("We've hidden the flag and your evil robots will never find it!"))
app.get('/robots.txt', (req, res) => res.send("super_secret_flag.txt"))
app.get('/super_secret_flag.txt', (req, res) => res.send("{{flag}}"))

app.listen(port)
