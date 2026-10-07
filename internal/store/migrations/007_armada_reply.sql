-- The rumor an Armada message replies to, so an edit can keep the reply line
-- it was posted with. Empty when it replies to nothing.
alter table armada_messages add column reply_to text not null default '';
