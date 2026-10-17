-- /pp's quests. A member's held quests (tier, kind, progress on each task,
-- and a selfie waiting out its day: comma separated, a quest each between
-- semicolons), the day they were last dealt one, the day they last
-- rerolled, and how many they've ever finished; and the channel selfie
-- quests count pictures in.
alter table pp_member
	add column quests      text   not null default '',
	add column quest_day   bigint not null default 0,
	add column reroll_day  bigint not null default 0,
	add column quests_done bigint not null default 0;

alter table pp_guild add column selfie_channel bigint not null default 0;
