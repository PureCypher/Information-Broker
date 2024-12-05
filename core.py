#!/usr/bin/python3

"""Main discord bot script which actually posts the articles to the channel in question"""


# Standard libraries
import aiohttp
import asyncio
import datetime
import sqlite3
# None standard libraries
from discord import Webhook, Embed, Color
import contentGrab as WEB

WEBHOOK_URLS = [
    "https://discord.com/api/webhooks/EXAMPLE/EXAMPLE", # Put you desired webhook here, this also can take multiple webhooks. 
    "https://discord.com/api/webhooks/EXAMPLE/EXAMPLE"
]

async def main():
    try:
        # Database connection
        connection = sqlite3.connect("./articles.db")
        cursor = connection.cursor()

        # Read last check counts
        try:
            with open("./check.txt", "r", encoding='UTF-8') as lastcheck_file:
                lines = lastcheck_file.read().split('\n')
            article_count, nist_count = (int(lines[1]), int(lines[0])) if len(lines) >= 2 else (0, 0)
        except FileNotFoundError:
            print("Warning: check.txt not found, initializing counters.")
            article_count, nist_count = 0, 0

        # SQL query to fetch articles
        query = """
        SELECT *
        FROM all_articles
        WHERE ID > ?
        ORDER BY 
            CAST(SUBSTR(published, 8, 4) AS INTEGER) ASC,
            CASE SUBSTR(published, 4, 3)
                WHEN 'Jan' THEN 1 WHEN 'Feb' THEN 2 WHEN 'Mar' THEN 3
                WHEN 'Apr' THEN 4 WHEN 'May' THEN 5 WHEN 'Jun' THEN 6
                WHEN 'Jul' THEN 7 WHEN 'Aug' THEN 8 WHEN 'Sep' THEN 9
                WHEN 'Oct' THEN 10 WHEN 'Nov' THEN 11 WHEN 'Dec' THEN 12
            END ASC,
            CAST(SUBSTR(published, 1, 2) AS INTEGER) ASC;
        """
        cursor.execute(query, (article_count,))
        records = cursor.fetchall()
        year = str(datetime.date.today().year)

        # Send articles to Discord webhooks
        async with aiohttp.ClientSession() as session:
            for url in WEBHOOK_URLS:
                webhook = Webhook.from_url(url, session=session)
                for art in records:
                    article_count = art[0]
                    if year in art[5]:
                        embed = Embed(
                            title=art[3],
                            description=art[6],
                            url=art[4],
                            color=Color.dark_blue()
                        )
                        embed.add_field(name="Published Date", value=art[5], inline=False)
                        try:
                            await webhook.send(embed=embed, username='Information Broker', avatar_url="https://vignette.wikia.nocookie.net/es.starwars/images/e/e5/Information_broker_TotG.jpg")
                        except Exception as webhook_error:
                            print(f"Failed to send to webhook {url}: {webhook_error}")
            with open('./check.txt', 'w+', encoding='UTF-8') as lastcheck:
                lastcheck.write(f'{nist_count}\n{article_count}')
    except Exception as e:
        print(f"Error occurred: {e}")
    finally:
        connection.close()

if __name__ == "__main__":
    WEB.scrape_feeds()
    asyncio.run(main())

