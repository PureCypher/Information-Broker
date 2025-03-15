#!/usr/bin/python3

"""
Scrape all sites for every article past & present and pass it through a Llama3.2 instance running locally to summarize in a TLDR.
"""

# Standard libraries
import sqlite3
from hashlib import sha256
from typing import Optional
import random
import logging
from datetime import datetime

# Third-party libraries
import requests
from bs4 import BeautifulSoup
from selenium import webdriver
from selenium.webdriver.chrome.service import Service
from selenium.webdriver.chrome.options import Options
from selenium.webdriver.common.by import By
from selenium.webdriver.support.ui import WebDriverWait
from selenium.webdriver.support import expected_conditions as EC
from selenium.common.exceptions import TimeoutException

# Setup logging
logging.basicConfig(
    filename='scraping.log',
    level=logging.DEBUG,
    format='%(asctime)s - %(levelname)s - %(message)s'
)

USER_AGENT = [
    "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/134.0.0.0 Safari/537.36",
    "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/42.0.2311.135 Safari/537.36 Edge/12.246",
    "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_11_2) AppleWebKit/601.3.9 (KHTML, like Gecko) Version/9.0.2 Safari/601.3.9",
]
DB_PATH = "./articles.db"
FEEDS_FILE_PATH = "./feeds.txt"
API_URL = "http://127.0.0.1:11434/api/generate"  # Put either localhost with Ollama
MODEL_NAME = "granite3.2:8b"  # Or the model name of the Llama3.2 instance

def setup_webdriver():
    """Sets up and returns a configured Chrome WebDriver."""
    chrome_options = Options()
    # Essential flags for headless mode
    chrome_options.add_argument("--headless=new")
    chrome_options.add_argument("--no-sandbox")
    chrome_options.add_argument("--disable-dev-shm-usage")
    # Remote debugging and DevTools configuration
    chrome_options.add_argument("--remote-debugging-port=9222")
    chrome_options.add_argument("--remote-debugging-address=0.0.0.0")
    # Basic configuration
    chrome_options.add_argument("--disable-extensions")
    chrome_options.add_argument(f"user-agent={random.choice(USER_AGENT)}")
    chrome_options.add_argument("--window-size=1920,1080")
    # Additional stability flags
    chrome_options.add_argument("--disable-web-security")
    chrome_options.add_argument("--ignore-certificate-errors")
    chrome_options.add_argument("--allow-running-insecure-content")
    # Memory and process handling
    chrome_options.add_argument("--disable-application-cache")
    chrome_options.add_argument("--aggressive-cache-discard")
    chrome_options.add_argument("--disable-browser-side-navigation")
    # Disable unnecessary features
    chrome_options.add_argument("--disable-gpu")
    chrome_options.add_argument("--disable-software-rasterizer")
    chrome_options.add_argument("--disable-dev-tools")
    # Create Service with specific log options
    service = Service(log_output=str("/dev/null"))
    # Initialize Chrome with both options and service
    return webdriver.Chrome(options=chrome_options, service=service)

def get_title(field: BeautifulSoup) -> str:
    """Extracts the title from an RSS field."""
    title = field.find('title')
    return title.text if title else 'N/A'

def get_description(field: BeautifulSoup) -> str:
    """Extracts the description from an RSS field."""
    description = field.find('description')
    return description.text if description else 'N/A'

def get_published_date(field: BeautifulSoup) -> str:
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
        response = requests.post(url=API_URL, json=prompt_data, headers={'User-Agent': random.choice(USER_AGENT)}, timeout=2000)
        response.raise_for_status()
        result = response.json()
        return result.get('response', 'N/A')
    except requests.RequestException as e:
        return None

def check_link(link: str) -> str:
    """Adjusts the link if it contains '/cybersecurity-blog/'."""
    if "/cybersecurity-blog/" in link:
        link = f'https://any.run{link}'
    return link

def validate_content(content: str) -> bool:
    """Validates if the extracted content meets quality criteria."""
    # Check if content is not empty or default value
    if not content or content == "N/A":
        return False
    # Check minimum content length (e.g., at least 100 characters)
    if len(content) < 100:
        return False
    # Check for common error indicators
    error_indicators = ["404", "Access Denied", "Forbidden"]
    if any(indicator in content for indicator in error_indicators):
        return False
    return True

def extract_article_content(url: str) -> str:
    """Fetches and extracts the main content of an article using Selenium."""
    driver = None
    try:
        logging.info(f"Attempting to fetch: {url}")
        driver = setup_webdriver()
        try:
            driver.get(url)
            logging.info("Successfully loaded URL")
        except Exception as e:
            logging.error(f"Error loading URL: {e}")
            return "N/A"
        try:
            # Different wait strategies based on the site
            site_specific_selectors = {
                "any.run": ("class", "entry-content single-post"), 
                "bleepingcomputer.com": ("class", "article_section"),
                "binarydefense.com": ("class", "TwoColumnLayout"),
                "thehackernews.com": ("class", "articlebody clear cf"),
                "darkreading.com": ("class", "TwoColumnLayout"),
                "krebsonsecurity.com": ("class", "wrapper"),
                "sophos.com": ("class", "content-area"),
                "truefort.com": ("class", "elementor-widget-container"),
                "socprime.com": ("class", "light-theme"),
                "canarytrap.com": ("class", "content-holder"),
                "socradar.io": ("class", "content-wrapper"),
                "nist.gov": ("class", "nist-block")
            }

            for domain, (selector_type, selector_value) in site_specific_selectors.items():
                if domain in url:
                    logging.info(f"Detected {domain}, waiting for {selector_value}")
                    WebDriverWait(driver, 30).until(
                        EC.presence_of_element_located((By.CLASS_NAME if selector_type == "class" else By.ID, selector_value))
                    )
                    break
            else:
                logging.info("Using generic wait strategy")
                WebDriverWait(driver, 30).until(
                    EC.presence_of_element_located((By.TAG_NAME, "article"))
                )
        except TimeoutException as e:
            logging.error(f"Timeout waiting for content: {e}")
            # Take screenshot on timeout
            try:
                timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
                pass
            except Exception as se:
                pass
            return "N/A"
        except Exception as e:
            logging.error(f"Error waiting for content: {e}")
            return "N/A"
        # Get page source after JavaScript execution
        try:
            page_source = driver.page_source
            logging.info(f"Got page source, length: {len(page_source)}")
        except Exception as e:
            logging.error(f"Error getting page source: {e}")
            return "N/A"
        # Parse with BeautifulSoup
        try:
            logging.info("Parsing with BeautifulSoup")
            soup = BeautifulSoup(page_source, 'html.parser')
            content = ""
            # Log page structure for debugging
            logging.debug("Page structure:")
            logging.debug(soup.prettify()[:1000])  # Log first 1000 chars of structure
            # Site-specific content extraction
            site_content_selectors = {
                "any.run": ("div", "entry-content__content js-content"),
                "bleepingcomputer.com": ("div", "articleBody"),
                "binarydefense.com": ("div", "ArticleBase-Body"),
                "thehackernews.com": ("div", "post-content"),
                "darkreading.com": ("div", "ArticleBase-Body"),
                "krebsonsecurity.com": ("div", "content"),
                "sophos.com": ("div", "content-area"),
                "truefort.com": ("div", "post-content"),
                "socprime.com": ("div", "sc-block__inner inner-xs"),
                "canarytrap.com": ("div", "blog-text"),
                "socradar.io": ("div", "content-wrapper"),
                "nist.gov": ("div", "text-with-summary")
            }

            for domain, (tag, class_name) in site_content_selectors.items():
                if domain in url:
                    logging.info(f"Attempting {domain} specific extraction")
                    content_container = soup.find(tag, class_=class_name)
                    if content_container:
                        logging.info(f"Found {class_name} container")
                        paragraphs = content_container.find_all(['p', 'h2', 'h3'], recursive=True)
                        content = " ".join(p.get_text(strip=True) for p in paragraphs if p.get_text(strip=True))
                        logging.info(f"Extracted {len(paragraphs)} paragraphs")
                        logging.debug(f"First paragraph: {paragraphs[0].get_text() if paragraphs else 'None'}")
                        break
            # If site-specific extraction fails, try generic selectors
            if not content:
                logging.info("Site-specific extraction failed, trying generic selectors")
                content_selectors = [
                    ('article', {}),
                    ('div', {'class_': ["articleBody", "post-content", "article-content", "entry-content", "post-content", "content-body", "field-item even"]}),
                    ('div', {'itemprop': "articleBody"}),
                    ('div', {'role': "main"})
                ]

                for tag, attrs in content_selectors:
                    logging.info(f"Trying selector: {tag} {attrs}")
                    container = soup.find(tag, attrs)
                    if container:
                        paragraphs = container.find_all(['p', 'h2', 'h3'], recursive=True)
                        if paragraphs:
                            content = " ".join(p.get_text(strip=True) for p in paragraphs if p.get_text(strip=True))
                            logging.info(f"Found content with selector {tag}")
                            logging.debug(f"First paragraph: {paragraphs[0].get_text() if paragraphs else 'None'}")
                            break
            if not content:
                logging.warning(f"No content found with any selectors - trying fallback")
                paragraphs = soup.find_all('p')
                content = " ".join(p.get_text(strip=True) for p in paragraphs if p.get_text(strip=True))
            if content:
                logging.info(f"Successfully extracted {len(content)} characters")
                logging.debug(f"Content preview: {content[:200]}...")
                # Validate content quality
                if validate_content(content):
                    logging.info(f"Content validation passed for {url}")
                    return content
                else:
                    logging.error(f"Content validation failed for {url}")
                    with open('failed_extractions.log', 'a') as f:
                        f.write(f"{datetime.now()} - Validation failed - {url}\n")
                    return "N/A"
            else:
                logging.error("No content could be extracted")
                with open('failed_extractions.log', 'a') as f:
                    f.write(f"{datetime.now()} - No content - {url}\n")
                return "N/A"
        except Exception as e:
            logging.error(f"Error during content extraction: {e}", exc_info=True)
            return "N/A"

    except TimeoutException:
        print(f"Timeout while loading {url}")
        return "N/A"
    except Exception as e:
        print(f"Error fetching article content from {url}: {e}")
        return "N/A"
    finally:
        if driver:
            try:
                driver.quit()
            except Exception:
                pass

def scrape_feeds():
    """Scrapes and processes articles from RSS feeds."""
    results = []
    stats = {
        'total_attempts': 0,
        'successful_extractions': 0,
        'failed_validations': 0,
        'error_extractions': 0
    }

    try:
        with open(FEEDS_FILE_PATH, 'r', encoding='UTF-8') as feed_file:
            for url in feed_file:
                url = url.strip()
                try:
                    response = requests.get(url, headers={'User-Agent': random.choice(USER_AGENT)}, timeout=600)
                    response.raise_for_status()
                    soup = BeautifulSoup(response.content, 'xml')
                    articles = soup.find_all('item')
                    for article in articles:
                        link = article.find('link').text
                        title = get_title(article)
                        published = get_published_date(article)
                        # Create hash for checking existence
                        h_string = f"{url}{title}{link}"
                        hashed = sha256(h_string.encode('UTF-8')).hexdigest()
                        table_name = 'nist' if url == 'http://nvd.nist.gov/download/nvd-rss.xml' else 'all_articles'
                        # Skip if article exists
                        if article_exists_in_db(hashed, table_name):
                            continue
                        tLink = check_link(link)
                        if url == "https://feeds.feedburner.com/TheHackersNews":
                            description = get_description(article)
                            summary = description
                        else:
                            stats['total_attempts'] += 1
                            description = extract_article_content(tLink)
                            if description != "N/A" and description:
                                stats['successful_extractions'] += 1
                            else:
                                stats['error_extractions'] += 1
                                logging.error(f"Failed to extract content from {tLink}")
                            summary = generate_summary(description) or "N/A"

                        insert_article(url, title, link, published, summary)
                        results.append({
                            'SITE_URL': url,
                            'TITLE': title,
                            'LINK': tLink,
                            'PUBLISHED': published,
                            'tldr': summary,
                        })

                except requests.RequestException as e:
                    print(f"Error processing feed {url}: {e}")
    finally:
        # Log final statistics
        if stats['total_attempts'] > 0:
            logging.info("\nContent Extraction Statistics:")
            logging.info(f"Total attempts: {stats['total_attempts']}")
            logging.info(f"Successful extractions: {stats['successful_extractions']}")
            logging.info(f"Failed extractions: {stats['error_extractions']}")
            logging.info(f"Success rate: {(stats['successful_extractions']/stats['total_attempts']*100):.2f}%")

    return results

if __name__ == "__main__":
    scrape_feeds()
