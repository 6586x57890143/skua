-- One row per module a guild's admins have turned off. No row is on.
create table module_off (
	guild_id bigint not null,
	module   text   not null,
	primary key (guild_id, module)
);
