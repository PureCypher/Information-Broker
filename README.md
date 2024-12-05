### Discord Bot setup

```bash
sudo apt install python3.11-venv
sudo apt install python3-pip

python3 -m venv venv
source venv/bin/activate
pip install -r requirements.txt
```

Requirements.txt
```
requests
beautifulsoup4
lxml
discord.py
```

Cronjob
```
crontab -e

0 9 * * * /home/{username}/start.sh >> /home/{username}/log_$(date +\%d-\%m-\%Y).txt 2>&1

0 8 * * * find /home/{username} -name "log_*.txt" -type f -mtime +7 -exec rm {} \;
```

