-- Where a guild's cards go unless a follow names its own channel.
create table notify_guild (
	guild_id   bigint primary key,
	channel_id bigint not null
);

-- One row per account a guild follows. channel_id 0 is the guild's channel
-- above; role_id 0 pings no one; name is how the account reads in /notify.
create table notify_follow (
	guild_id   bigint not null,
	channel_id bigint not null,
	platform   text   not null,
	account    text   not null,
	name       text   not null,
	role_id    bigint not null default 0,
	primary key (guild_id, platform, account, channel_id)
);

-- What notify last saw of an account, however many guilds follow it, so a
-- restart neither repeats an alert nor takes a creator's backlog for news.
create table notify_seen (
	platform text   not null,
	account  text   not null,
	ids      text[] not null,
	primary key (platform, account)
);
