-- The dyers' yard: whether it sells to everyone yet, each livery's role in
-- a server once it's made, and the livery a member leases and the day its
-- lease runs out.
alter table pp_guild add column yard_open boolean not null default false;

create table pp_dye (
	guild_id bigint not null,
	key      text   not null,
	role_id  bigint not null,
	primary key (guild_id, key)
);

alter table pp_member
	add column livery       text   not null default '',
	add column livery_until bigint not null default 0;
