
"""LINTER"""

import sqlite3

connection = sqlite3.connect("articles.db")

cursor = connection.cursor()
cursor.execute("CREATE TABLE nist (id INTEGER PRIMARY KEY AUTOINCREMENT, hash TEXT, site_url TEXT, title TEXT, link TEXT, published TEXT, tldr TEXT)")
cursor.execute("CREATE TABLE all_articles (id INTEGER PRIMARY KEY AUTOINCREMENT, hash TEXT, site_url TEXT, title TEXT, link TEXT, published TEXT, tldr TEXT)")
cursor.close()
