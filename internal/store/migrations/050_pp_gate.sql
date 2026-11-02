-- Whether the north gate lets every bird out on forays yet, not only
-- admins: an admin opens it once, from /pp's help page.
alter table pp_guild add column gate_open boolean not null default false;
