-- The gate book: every bird who joined or left a server since sureties
-- began, who stands surety for them (0 for nobody), the day they joined,
-- the day they left (0 while they're here) and the last day they earned
-- while here (0 for never).
create table pp_guest (
	guild_id bigint not null,
	user_id  bigint not null,
	by_id    bigint not null,
	day      bigint not null,
	gone     bigint not null default 0,
	seen     bigint not null default 0,
	primary key (guild_id, user_id)
);
