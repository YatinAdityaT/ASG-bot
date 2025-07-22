*ASG Bot Design document*

List of features I had in mind:

Current capabilities:
1. The bot can only read messages of the groups it is part of
2. The bot has amnesia for group messages. It receives and sees all messages in a group, but none of them are stored. 
3. After seeing a message, the bot checks if it was meant for it to respond to or not. If not, the bot forgets about that message - messages aren't persisted in a database 
4. The bot is a simple (non fuzzy) bot - no LLM integrations yet (we probably don't need them). 
5. Works asynchronously across multiple groups - Just needs to added to groups to access them

Capabilities I will add soon:
1. For the accountability subscribers group, the bot will automatically ping people who want to be pinged, and it will share who joined and left the call. It will also auto ping if someone is alone in the call
2. /help - will list all the commands it responds to
3. /remind - will remind you of stuff.. could be used in the todo group. Could also be used to get reminders to read or respond to certain messages. Later, I might integrate SMS or calling as well, depending on demand 
4. /mood - will keep track of a person's mood. Could help get the overall mood of the group or could be later integrated with other tools to monitor how everyone is feeling. Will provide persistence to people's feelings data. Could be used to observe trends per person or as a group
5. /feedback - will take feedback and store it for later review. Feedback could be anything about the bot or the group itself
6. /agenda - could be used with Google Calendar to list out a group level agenda or a user level agenda into a given group
7. Auto responses: These will be responses that the bot sends without being explicitly prompted. I don't want to add too many of these as they would be spammy. Some ideas include - auto-response-tone-indicators - which will automatically post a message explaining a tone indicator the moment one has been used. 
8. I am yet to brainstorm other ideas of what this bot could be used for. Very open to suggestions

Notes: 
1. Since this code will be running on my pc for now (unless I rent some server) the uptime might vary with how / when I turn off my pc
2. I can open source the entire code of this bot so that people can read the code, add to it or critic it
3. I choose the image of Mo from WallE because he is really cute bot. This could possibly cause confusion with some folk as he looks rather mad half of the time - this bot won't have Mo like features (getting angry and shit). I just really like Mo
4. Rate limiting - I'll have to have some form of rate limiting with this bot as such bots go against WhatsApp's TOS (because they want to sell you their WhatsApp business API). So the bot would rate limit (it won't respond for some time) anyone who tries to ping it one too many times in a short duration. This is to protect the bot from being overwhelmed and to not trigger WhatsApp's TOS  
5. Future plans: The sky is the limit with this bot tbh. We could look into integrations with different tools and services to make most of the managerial tasks easier to handle. The ADHD manager could also be given tools that will help manage all the folk.