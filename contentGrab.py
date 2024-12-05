#!/usr/bin/python3

"""
Scrape all sites for every article past & present and pass it through a Llama3.2 instance running locally to summarize in a TLDR. 
"""

# Standard libraries
import sqlite3
from hashlib import sha256
from typing import Optional

# Third-party libraries
import requests
from bs4 import BeautifulSoup


USER_AGENT = {
    'User-Agent': 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/116.0.0.0 Safari/537.36'
}
IPADDRESS = "127.0.0.1" # change this to what ever you need
DB_PATH = "./articles.db"
FEEDS_FILE_PATH = "./feeds.txt"
API_URL = "http://{IPADDRESS}:11434/api/generate" # Put either localhost with Ollama 
MODEL_NAME = "llama3.2"


def get_title(field: BeautifulSoup) -> str:
    """Extracts the title from an RSS field."""
    title = field.find('title')
    return title.text if title else 'N/A'


def get_published_date(field: BeautifulSoup, url: str) -> str:
    """Extracts the publication date from an RSS field."""
    pub_date = field.find('pubDate')
    date_parts = pub_date.text.split()
    
    if len(date_parts) == 6:  # Format: 'Mon, 04 Nov 2024 18:47:32 -0500'
        return f"{date_parts[1]}/{date_parts[2]}/{date_parts[3]}"
    elif len(date_parts) == 5:  # Format: '30 Oct 2024 07:30:08'
        return f"{date_parts[0]}/{date_parts[1]}/{date_parts[2]}"
    elif len(date_parts) >= 3:  # Fallback
        return f"{date_parts[0]}/{date_parts[1]}/{date_parts[2]}"
    return 'N/A'


def article_exists_in_db(hash_id: str, table_name: str) -> bool:
    """Checks if an article with the given hash already exists in the database."""
    query = f"SELECT 1 FROM {table_name} WHERE hash = ?"
    with sqlite3.connect(DB_PATH) as connection:
        cursor = connection.cursor()
        cursor.execute(query, (hash_id,))
        return bool(cursor.fetchone())


def insert_article(url: str, title: str, link: str, published: str, tldr: str) -> None:
    """Inserts a unique article into the database if it does not exist."""
    h_string = f"{url}{title}{link}"
    hashed = sha256(h_string.encode('UTF-8')).hexdigest()
    
    table_name = 'nist' if url == 'http://nvd.nist.gov/download/nvd-rss.xml' else 'all_articles'
    
    if article_exists_in_db(hashed, table_name):
        return
    
    query = f"INSERT INTO {table_name} (hash, site_url, title, link, published, tldr) VALUES (?, ?, ?, ?, ?, ?)"
    with sqlite3.connect(DB_PATH) as connection:
        cursor = connection.cursor()
        cursor.execute(query, (hashed, url, title, link, published, tldr))
        connection.commit()


def generate_summary(description: str) -> Optional[str]:
    """Generates a summary using an API call."""
    prompt_data = {
        "model": MODEL_NAME,
        "prompt": f"summarize the following article into a quick readable form factor with just the summary posted and nothing else: {description}",
        "stream": False
    }
    try:
        response = requests.post(url=API_URL, json=prompt_data, headers=USER_AGENT, timeout=2000)
        response.raise_for_status()
        result = response.json()
        return result.get('response', 'N/A')
    except requests.RequestException as e:
        return None


def check_link(link:str) -> str:
    """Adjusts the link if it contains '/cybersecurity-blog/'."""
    if "/cybersecurity-blog/" in link:
        link = f'https://any.run/{link}'
    return link


def extract_article_content(url: str) -> str:
    """Fetches and extracts the main content of an article given its URL."""
    try:
        response = requests.get(url, headers=USER_AGENT, timeout=600)
        response.raise_for_status()
        
        soup = BeautifulSoup(response.content, 'html.parser')
        content_tags = soup.find('article') or soup.find('div', class_="main-content") or soup.find_all('p')
        
        if content_tags:
            main_text = " ".join([tag.get_text(strip=True) for tag in content_tags])
            return main_text
        return "N/A"
        
    except requests.RequestException as e:
        return "N/A"


def scrape_feeds():
    """Scrapes and processes articles from RSS feeds."""
    results = []
    with open(FEEDS_FILE_PATH, 'r', encoding='UTF-8') as feed_file:
        for url in feed_file:
            url = url.strip()
            try:
                response = requests.get(url, headers=USER_AGENT, timeout=600)
                response.raise_for_status()
                
                soup = BeautifulSoup(response.content, 'xml')
                articles = soup.find_all('item')
                
                for article in articles:
                    link = article.find('link').text
                    title = get_title(article)
                    published = get_published_date(article, url)
                    
                    # Create hash for checking existence
                    h_string = f"{url}{title}{link}"
                    hashed = sha256(h_string.encode('UTF-8')).hexdigest()
                    table_name = 'nist' if url == 'http://nvd.nist.gov/download/nvd-rss.xml' else 'all_articles'
                    
                    # Skip if article exists
                    if article_exists_in_db(hashed, table_name):
                        continue
                    tLink = check_link(link)
                    description = extract_article_content(tLink)
                    summary = generate_summary(description) or "N/A"
                    insert_article(url, title, link, published, summary)
                    
                    results.append({
                        'SITE_URL': url,
                        'TITLE': title,
                        'LINK': tLink,
                        'PUBLISHED': published,
                        'tldr': summary
                    })
            except requests.RequestException as e:
                print(f"Error processing feed {url}: {e}")
