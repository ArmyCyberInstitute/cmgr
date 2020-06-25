<?php
$FLAG = "{{flag}}";
$database_file = "./users.db";
$table_init = file_exists($database_file);
$con = new SQLite3($database_file);

if ( ! $table_init ) {
    $con->exec("CREATE TABLE IF NOT EXISTS users (name TEXT NOT NULL PRIMARY KEY, password TEXT, admin INTEGER);");
    $con->exec("INSERT INTO users VALUES ('admin', 'pbkdf2:sha1:1000$bTY1abU0$5503ae46ff1a45b14ff19d5a2ae08acf1d2aacde', 1);");
}
?>
