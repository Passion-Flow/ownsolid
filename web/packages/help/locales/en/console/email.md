# Email

The installation sends invitations, password resets and notices of password changes through your
mail server. [Email](/mail) shows each message's delivery.

## Delivery states

| State     | Meaning                                                                              |
| --------- | ------------------------------------------------------------------------------------ |
| Waiting   | Waiting to be sent, or waiting for its next attempt.                                 |
| Sending   | Being handed to the mail server now.                                                 |
| Sent      | The mail server accepted it.                                                         |
| Failed    | Every attempt failed. The error says why, such as the mail server being unreachable. |
| Cancelled | It will not be sent.                                                                 |

## Sending again

Select failed messages and choose **Send again**; a new message is queued for each. An invitation can
also be revoked from here by typing its address.
