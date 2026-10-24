-- Petitions to the heralds for a livery of one's own: what was asked for,
-- what's held from the purse while they decide, the post in their
-- channel, and how it was settled. state is asked, granting (claimed by a
-- moderator), granted, refused, changed (a later grant replaced it) or
-- lapsed (its lease ran out and the role came off).
create table pp_petition (
	id         bigserial primary key,
	guild_id   bigint  not null,
	user_id    bigint  not null,
	name       text    not null,
	style      text    not null,
	colors     bigint[] not null default '{}',
	icon       bytea,
	state      text    not null,
	held       bigint  not null,
	change     boolean not null default false,
	asked      bigint  not null,
	post       bigint  not null default 0,
	decided_by bigint  not null default 0,
	reason     text    not null default '',
	role_id    bigint  not null default 0
);

create index pp_petition_member on pp_petition (guild_id, user_id);
