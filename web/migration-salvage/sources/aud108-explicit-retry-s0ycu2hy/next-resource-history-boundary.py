from datetime import datetime,timedelta,timezone
import json
now=datetime(2026,11,13,tzinfo=timezone.utc)
rows=[]
for age in [0.25,7,30,31,32,365]:
 created=now-timedelta(days=age)
 lifetime=now-(created-timedelta(minutes=1))
 rows.append({'resourceAgeDays':age,'currentLifetimeQueryDays':lifetime.total_seconds()/86400,'acceptedByDefault31DayMaximum':timedelta(0)<lifetime<=timedelta(days=31),'explicitLast24HoursAcceptedByMinimumValidPolicy':timedelta(days=1)<=timedelta(days=1)})
print(json.dumps({'scope':'Static time-bound calculation using the current parser constraints; not a server, browser or SQL run','cases':rows,'observations':['Creation-minus-one-minute exceeds even the default maximum at exactly31days','A recent24hour window fits every valid maximum, but a fixed choice must not hide older history','A server-default window avoids guessed policy; the response does not expose the chosen window','Cursor windows are pinned and require consistent bounds; explicit recovery must remain one request']},indent=2))
