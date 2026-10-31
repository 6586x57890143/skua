-- The slack tide: the house's hold, whether the counting house's note has
-- seeded it, and the channel its tables are posted in; and each member's
-- stake sat at a table not yet thrown, and what they've staked today.
alter table pp_guild add column hold bigint not null default 0;
alter table pp_guild add column house_noted boolean not null default false;
alter table pp_guild add column house_channel bigint not null default 0;
alter table pp_member add column seat bigint not null default 0;
alter table pp_member add column staked bigint not null default 0;
