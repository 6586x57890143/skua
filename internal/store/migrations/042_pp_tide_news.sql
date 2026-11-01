-- Where the slack tide's news goes (the bell, all hands, a duel called
-- out), 0 for beside its tables in house_channel.
alter table pp_guild add column tide_news bigint not null default 0;
