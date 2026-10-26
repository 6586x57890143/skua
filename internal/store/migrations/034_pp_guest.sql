-- The gate book: everyone who joined a server since sureties began, who
-- stands surety for them (0 for nobody), the day they joined and the day
-- they left (0 while they're here).
create table pp_guest (
	guild_id bigint not null,
	user_id  bigint not null,
	by_id    bigint not null,
	day      bigint not null,
	gone     bigint not null default 0,
	primary key (guild_id, user_id)
);
